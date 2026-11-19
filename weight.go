//go:build windows

package main

// Mede o peso do Farmador inteiro: o próprio app + todos os processos do motor do Edge
// (WebView2) que ele abriu para as páginas da Kick. Soma a memória privada e o uso de CPU.

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	pCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	pProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	pProcess32NextW           = kernel32.NewProc("Process32NextW")
	pOpenProcess              = kernel32.NewProc("OpenProcess")
	pGetProcessTimes          = kernel32.NewProc("GetProcessTimes")
	pK32GetProcessMemoryInfo  = kernel32.NewProc("K32GetProcessMemoryInfo")

	wLastCPU  uint64
	wLastAt   time.Time
	wText     = "medindo…"
	wTextLine = ""
	wDetails  []string
)

type procEntry struct {
	Size         uint32
	Usage        uint32
	PID          uint32
	HeapID       uintptr
	ModuleID     uint32
	Threads      uint32
	ParentPID    uint32
	PriClassBase int32
	Flags        uint32
	ExeFile      [260]uint16
}

type memCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

// myTree devolve o PID do Farmador e de todos os processos "filhos" (motor do Edge).
func myTree() []uint32 {
	self := uint32(os.Getpid())
	snap, _, _ := pCreateToolhelp32Snapshot.Call(2, 0) // TH32CS_SNAPPROCESS
	if snap == ^uintptr(0) {
		return []uint32{self}
	}
	defer syscall.CloseHandle(syscall.Handle(snap))
	kids := map[uint32][]uint32{}
	var e procEntry
	e.Size = uint32(unsafe.Sizeof(e))
	for r, _, _ := pProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&e))); r != 0; r, _, _ = pProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&e))) {
		if e.PID != e.ParentPID {
			kids[e.ParentPID] = append(kids[e.ParentPID], e.PID)
		}
	}
	out := []uint32{self}
	seen := map[uint32]bool{self: true}
	for i := 0; i < len(out); i++ {
		for _, k := range kids[out[i]] {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

func procUsage(pid uint32) (mem uint64, cpu uint64, ok bool) {
	h, _, _ := pOpenProcess.Call(0x1000, 0, uintptr(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if h == 0 {
		return 0, 0, false
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	var mc memCounters
	mc.Cb = uint32(unsafe.Sizeof(mc))
	if r, _, _ := pK32GetProcessMemoryInfo.Call(h, uintptr(unsafe.Pointer(&mc)), uintptr(mc.Cb)); r != 0 {
		mem = uint64(mc.PrivateUsage)
	}
	var c, x, k, u syscall.Filetime
	if r, _, _ := pGetProcessTimes.Call(h, uintptr(unsafe.Pointer(&c)), uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u))); r != 0 {
		cpu = (uint64(k.HighDateTime)<<32 | uint64(k.LowDateTime)) + (uint64(u.HighDateTime)<<32 | uint64(u.LowDateTime))
	}
	return mem, cpu, true
}

func mb(b uint64) string {
	m := float64(b) / (1 << 20)
	if m >= 1024 {
		return fmt.Sprintf("%.1f GB", m/1024)
	}
	return fmt.Sprintf("%.0f MB", m)
}

// measureWeight atualiza o texto de peso (chamado a cada 2 s com as configurações abertas).
func measureWeight() {
	pids := myTree()
	var appMem, edgeMem, cpu uint64
	n := 0
	for i, p := range pids {
		m, c, ok := procUsage(p)
		if !ok {
			continue
		}
		cpu += c
		if i == 0 {
			appMem = m
		} else {
			edgeMem += m
			n++
		}
	}
	now := time.Now()
	pct := -1.0
	if !wLastAt.IsZero() && cpu >= wLastCPU {
		el := now.Sub(wLastAt).Seconds()
		if el > 0 {
			pct = float64(cpu-wLastCPU) / 1e7 / el / float64(runtime.NumCPU()) * 100 // FILETIME = 100 ns
		}
	}
	wLastCPU, wLastAt = cpu, now
	cpuTxt := "…"
	if pct >= 0 {
		cpuTxt = fmt.Sprintf("%.0f%%", pct)
	}
	wText = fmt.Sprintf("%s de RAM · CPU %s", mb(appMem+edgeMem), cpuTxt)
	stMu.Lock()
	open := len(kickWatchLive)
	stMu.Unlock()
	_ = open
	wTextLine = fmt.Sprintf("Farmador %s · lives %s · %d páginas", mb(appMem), mb(edgeMem), len(kpages))
	// detalhes (quadro ao passar o mouse)
	per := map[string]int{}
	for _, p := range kpages {
		per[fmt.Sprintf("%s%d", p.plat, p.acct)]++
	}
	cfgMu.Lock()
	var accLines []string
	for i := 0; i < 4; i++ {
		st := func(user string, watch bool) string {
			switch {
			case user == "":
				return "sem login"
			case i == 0 || watch:
				return "assistindo"
			}
			return "pausada"
		}
		name := cfg.KickAccs[i].Conta
		if name == "" {
			name = fmt.Sprintf("Conta %02d", i+1)
		}
		accLines = append(accLines, fmt.Sprintf("%02d %s: Kick %d (%s) · Twitch %d (%s)", i+1, name,
			per[fmt.Sprintf("kick%d", i)], st(cfg.KickAccs[i].User, cfg.KickAccs[i].Watch),
			per[fmt.Sprintf("tw%d", i)], st(cfg.TwAccs[i].User, cfg.TwAccs[i].Watch)))
	}
	cfgMu.Unlock()
	wDetails = append([]string{
		"RAM total: " + mb(appMem+edgeMem) + " · CPU " + cpuTxt,
		"Farmador (o app): " + mb(appMem),
		fmt.Sprintf("Kick e Twitch (motor do Edge): %s em %d processos", mb(edgeMem), n),
		fmt.Sprintf("Páginas abertas: %d (Kick ao vivo: %d · Twitch ao vivo: %d)", len(kpages), open, twLiveN()),
	}, accLines...)
}

func twLiveN() int {
	twMu.Lock()
	defer twMu.Unlock()
	return len(twLive)
}
