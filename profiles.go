//go:build windows

package main

// Um navegador só (motor do Edge) para as 4 contas da Kick, como o hub fazia:
// a conta 01 usa o perfil padrão (mantém o login de antes) e as contas 02–04 viram
// perfis dentro do mesmo navegador (ICoreWebView2Environment10). Se o Edge do PC for
// antigo e não aceitar perfis, volta sozinho ao jeito antigo (um navegador por conta).

import (
	"reflect"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
)

var (
	iidEnv10   = guid{0xee0eb9df, 0x6f12, 0x46ce, [8]byte{0xb5, 0x3f, 0x3f, 0x47, 0xb9, 0xc9, 0x28, 0xe0}}
	anchor     *edge.Chromium // página em branco da conta 01 que dá acesso ao navegador compartilhado
	anchorHwnd uintptr
	env10      uintptr
	profTried  bool
	profOK     bool
)

// índices na tabela de funções (vtable) — conferidos com a documentação da Microsoft
const (
	vtQueryInterface      = 0
	vtRelease             = 2
	vtEnv10CreateOptions  = 20 // ICoreWebView2Environment10::CreateCoreWebView2ControllerOptions
	vtEnv10CreateWithOpts = 21 // ICoreWebView2Environment10::CreateCoreWebView2ControllerWithOptions
	vtOptsPutProfileName  = 4  // ICoreWebView2ControllerOptions::put_ProfileName
)

func comCall(obj uintptr, idx int, args ...uintptr) uintptr {
	vt := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vt + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

// profilesOK prepara o navegador compartilhado (uma vez) e diz se dá para usar perfis.
func profilesOK() bool {
	if profTried {
		return profOK
	}
	profTried = true
	anchorHwnd, _, _ = pCreateWindowExW.Call(0x00000080|0x08000000, uintptr(unsafe.Pointer(clsHost)), uintptr(unsafe.Pointer(u16("Farmador · base"))),
		0x80000000, ^uintptr(3999), ^uintptr(3999), 64, 64, 0, 0, hInst, 0) // fora da tela
	if anchorHwnd == 0 {
		return false
	}
	ch := edge.NewChromium()
	ch.DataPath = kickDataDir(0)
	if !ch.Embed(anchorHwnd) || ch.GetController() == nil {
		return false
	}
	ch.Navigate("about:blank")
	anchor = ch
	env := ch.Environment()
	if env == nil {
		return false
	}
	var e10 uintptr
	if hr := comCall(uintptr(unsafe.Pointer(env)), vtQueryInterface, uintptr(unsafe.Pointer(&iidEnv10)), uintptr(unsafe.Pointer(&e10))); hr != 0 || e10 == 0 {
		return false // Edge antigo: sem perfis
	}
	env10 = e10
	profOK = true
	return true
}

// setUnexported escreve num campo interno da biblioteca (hwnd da janela).
func fieldPtr(ch *edge.Chromium, name string) reflect.Value {
	f := reflect.ValueOf(ch).Elem().FieldByName(name)
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

// embedProfile cria o navegador da página dentro do perfil indicado do navegador compartilhado.
// Reaproveita o tratamento da biblioteca (mensagens, permissões) usando o mesmo "avisador".
func embedProfile(ch *edge.Chromium, hwnd uintptr, profile string) bool {
	if !profOK || env10 == 0 {
		return false
	}
	fieldPtr(ch, "hwnd").SetUint(uint64(hwnd))
	handler := fieldPtr(ch, "controllerCompleted").Pointer()
	if handler == 0 {
		return false
	}
	var opts uintptr
	if hr := comCall(env10, vtEnv10CreateOptions, uintptr(unsafe.Pointer(&opts))); hr != 0 || opts == 0 {
		return false
	}
	defer comCall(opts, vtRelease)
	comCall(opts, vtOptsPutProfileName, uintptr(unsafe.Pointer(u16(profile))))
	if hr := comCall(env10, vtEnv10CreateWithOpts, hwnd, opts, handler); hr != 0 {
		return false
	}
	// espera o navegador ficar pronto, atendendo as mensagens da janela (como a biblioteca faz)
	deadline := time.Now().Add(20 * time.Second)
	var m msgT
	for ch.GetController() == nil && time.Now().Before(deadline) {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			pPostQuitMessage.Call(0)
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return ch.GetController() != nil
}
