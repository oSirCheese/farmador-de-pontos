//go:build windows

package main

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	comdlg32          = syscall.NewLazyDLL("comdlg32.dll")
	pGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	pCreateMutexW     = kernel32.NewProc("CreateMutexW")
	pFindWindowW      = user32.NewProc("FindWindowW")
	instanceMutex     uintptr
	errAlreadyExists  = syscall.Errno(183)
)

// singleInstance: só um Farmador por vez (duas cópias brigariam pelas sessões da Kick).
// Na troca de versão, espera a cópia antiga fechar.
func singleInstance() bool {
	for i := 0; i < 20; i++ {
		h, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(u16("Local\\FarmadorOSirCheese"))))
		if err != errAlreadyExists {
			instanceMutex = h
			return true
		}
		syscall.CloseHandle(syscall.Handle(h))
		if i == 0 && !strings.Contains(strings.Join(os.Args, " "), "--depois-de-atualizar") {
			if w, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(u16("FarmadorBalao"))), 0); w != 0 {
				pShowWindow.Call(w, 9) // já aberto: só traz para a frente
				pSetForegroundWindow.Call(w)
				return false
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// openFileDialog abre a janela do Windows para escolher um arquivo.
func openFileDialog(owner uintptr, title, filterName, pattern string) string {
	buf := make([]uint16, 1024)
	filter := []uint16{}
	for _, s := range []string{filterName, pattern, "Todos os arquivos", "*.*"} {
		p, _ := syscall.UTF16FromString(s)
		filter = append(filter, p...)
	}
	filter = append(filter, 0)
	ofn := struct {
		StructSize    uint32
		Owner         uintptr
		Instance      uintptr
		Filter        *uint16
		CustomFilter  *uint16
		MaxCustFilter uint32
		FilterIndex   uint32
		File          *uint16
		MaxFile       uint32
		FileTitle     *uint16
		MaxFileTitle  uint32
		InitialDir    *uint16
		Title         *uint16
		Flags         uint32
		FileOffset    uint16
		FileExtension uint16
		DefExt        *uint16
		CustData      uintptr
		FnHook        uintptr
		TemplateName  *uint16
		PvReserved    uintptr
		DwReserved    uint32
		FlagsEx       uint32
	}{Owner: owner, Filter: &filter[0], FilterIndex: 1, File: &buf[0], MaxFile: uint32(len(buf)), Title: u16(title),
		Flags: 0x00001000 | 0x00000800 | 0x00080000 | 0x00000008} // FILEMUSTEXIST|PATHMUSTEXIST|EXPLORER|NOCHANGEDIR
	if home, err := os.UserHomeDir(); err == nil {
		ofn.InitialDir = u16(filepath.Join(home, "Desktop"))
	}
	ofn.StructSize = uint32(unsafe.Sizeof(ofn))
	if r, _, _ := pGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn))); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// cleanupUpdate apaga o .exe antigo que ficou da última atualização.
func cleanupUpdate() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	os.Remove(exe + ".antigo")
}

// updateFromZip troca o .exe pelo que está dentro do zip e reabre o Farmador.
// As configurações (kick-timer.json) e os logins ficam onde estão.
func updateFromZip(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return errors.New("não consegui abrir o zip")
	}
	defer zr.Close()
	var f *zip.File
	for _, z := range zr.File {
		n := strings.ToLower(filepath.Base(z.Name))
		if strings.HasSuffix(n, ".exe") && strings.Contains(n, "farmador") {
			f = z
			break
		}
	}
	if f == nil {
		return errors.New("o zip não tem o .exe do Farmador")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp := exe + ".novo"
	out, err := os.Create(tmp)
	if err != nil {
		return errors.New("sem permissão para gravar na pasta do Farmador")
	}
	n, err := io.Copy(out, rc)
	out.Close()
	if err != nil || n < 1<<20 {
		os.Remove(tmp)
		return errors.New("o .exe dentro do zip parece incompleto")
	}
	// o Windows deixa renomear o .exe que está rodando
	os.Remove(exe + ".antigo")
	if err := os.Rename(exe, exe+".antigo"); err != nil {
		os.Remove(tmp)
		return errors.New("não consegui trocar o .exe: " + err.Error())
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(exe+".antigo", exe)
		return errors.New("não consegui trocar o .exe: " + err.Error())
	}
	// novos arquivos do zip (ex.: LEIA-ME) também vão para a pasta, sem tocar nas configurações
	dir := filepath.Dir(exe)
	for _, z := range zr.File {
		base := filepath.Base(z.Name)
		low := strings.ToLower(base)
		if z.FileInfo().IsDir() || z == f || strings.HasSuffix(low, ".json") || strings.HasSuffix(low, ".exe") {
			continue
		}
		if r, err := z.Open(); err == nil {
			if w, err := os.Create(filepath.Join(dir, base)); err == nil {
				io.Copy(w, r)
				w.Close()
			}
			r.Close()
		}
	}
	saveConfig()
	cmd := exec.Command(exe, "--depois-de-atualizar")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return errors.New("atualizado, mas não consegui reabrir: abra o Farmador de novo")
	}
	return nil
}

// ---------- atualizar sozinho pelo zip em Downloads / Área de Trabalho ----------

var (
	pSHGetKnownFolderPath = shell32.NewProc("SHGetKnownFolderPath")
	pCoTaskMemFree        = syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree")
	pTrackMouseEvent      = user32.NewProc("TrackMouseEvent")
	updMu                 sync.Mutex
	updPath, updVer       string
	updWhere              string
)

type guid struct {
	D1     uint32
	D2, D3 uint16
	D4     [8]byte
}

var (
	folderDownloads = guid{0x374DE290, 0x123F, 0x4565, [8]byte{0x91, 0x64, 0x39, 0xC4, 0x92, 0x5E, 0x46, 0x7B}}
	folderDesktop   = guid{0xB4BFCC3A, 0xDB2C, 0x424C, [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41}}
	updZipRe        = regexp.MustCompile(`(?i)^farmador-osircheese-v(\d+)\.(\d+)(?:\.(\d+))?.*\.zip$`)
)

func knownFolder(id *guid) string {
	var p *uint16
	if r, _, _ := pSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(id)), 0, 0, uintptr(unsafe.Pointer(&p))); r != 0 || p == nil {
		return ""
	}
	defer pCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return syscall.UTF16ToString((*[32768]uint16)(unsafe.Pointer(p))[:])
}

func verParts(v string) [3]int {
	var out [3]int
	for i, s := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(s)
	}
	return out
}

func verNewer(a, b string) bool {
	x, y := verParts(a), verParts(b)
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// scanUpdate procura um zip do Farmador mais novo que o atual (roda no monitor, a cada minuto).
func scanUpdate() {
	bestPath, bestVer, bestWhere := "", "", ""
	for _, f := range []struct {
		id    *guid
		where string
	}{{&folderDownloads, "Downloads"}, {&folderDesktop, "Área de Trabalho"}} {
		dir := knownFolder(f.id)
		if dir == "" {
			continue
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			m := updZipRe.FindStringSubmatch(e.Name())
			if m == nil || e.IsDir() {
				continue
			}
			v := m[1] + "." + m[2]
			if m[3] != "" {
				v += "." + m[3]
			}
			if verNewer(v, appVersion) && (bestVer == "" || verNewer(v, bestVer)) {
				bestPath, bestVer, bestWhere = filepath.Join(dir, e.Name()), v, f.where
			}
		}
	}
	updMu.Lock()
	changed := bestVer != updVer
	updPath, updVer, updWhere = bestPath, bestVer, bestWhere
	updMu.Unlock()
	if changed {
		onUpdate()
	}
}

func updFoundInfo() (string, string) {
	updMu.Lock()
	defer updMu.Unlock()
	return updVer, updWhere
}

// applyFoundUpdate instala o zip achado e reabre o Farmador.
func applyFoundUpdate(owner uintptr) {
	updMu.Lock()
	p := updPath
	updMu.Unlock()
	if p == "" {
		return
	}
	if hwndSet != 0 {
		readSettings()
	}
	if err := updateFromZip(p); err != nil {
		msgBox(owner, "Não deu para atualizar: "+err.Error(), appName, MB_ICONWARN)
		return
	}
	quitApp()
}
