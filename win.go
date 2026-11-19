//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")

	pRegisterClassExW     = user32.NewProc("RegisterClassExW")
	pCreateWindowExW      = user32.NewProc("CreateWindowExW")
	pDefWindowProcW       = user32.NewProc("DefWindowProcW")
	pGetMessageW          = user32.NewProc("GetMessageW")
	pTranslateMessage     = user32.NewProc("TranslateMessage")
	pDispatchMessageW     = user32.NewProc("DispatchMessageW")
	pIsDialogMessageW     = user32.NewProc("IsDialogMessageW")
	pPostQuitMessage      = user32.NewProc("PostQuitMessage")
	pPostMessageW         = user32.NewProc("PostMessageW")
	pSendMessageW         = user32.NewProc("SendMessageW")
	pSetWindowTextW       = user32.NewProc("SetWindowTextW")
	pGetWindowTextW       = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	pLoadCursorW          = user32.NewProc("LoadCursorW")
	pLoadIconW            = user32.NewProc("LoadIconW")
	pLoadImageW           = user32.NewProc("LoadImageW")
	pMessageBoxW          = user32.NewProc("MessageBoxW")
	pEnableWindow         = user32.NewProc("EnableWindow")
	pGetSysColorBrush     = user32.NewProc("GetSysColorBrush")
	pDestroyWindow        = user32.NewProc("DestroyWindow")
	pShowWindow           = user32.NewProc("ShowWindow")
	pCreateFontW          = gdi32.NewProc("CreateFontW")
	pShellExecuteW        = shell32.NewProc("ShellExecuteW")
	pGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	pCreateActCtxW        = kernel32.NewProc("CreateActCtxW")
	pActivateActCtx       = kernel32.NewProc("ActivateActCtx")
	pInitCommonControls   = comctl32.NewProc("InitCommonControls")
)

const (
	WS_OVERLAPPED    = 0x00000000
	WS_CAPTION       = 0x00C00000
	WS_SYSMENU       = 0x00080000
	WS_MINIMIZEBOX   = 0x00020000
	WS_VISIBLE       = 0x10000000
	WS_CHILD         = 0x40000000
	WS_TABSTOP       = 0x00010000
	WS_VSCROLL       = 0x00200000
	WS_BORDER        = 0x00800000
	WS_EX_CLIENTEDGE = 0x00000200

	ES_MULTILINE   = 0x0004
	ES_PASSWORD    = 0x0020
	ES_AUTOVSCROLL = 0x0040
	ES_AUTOHSCROLL = 0x0080
	ES_READONLY    = 0x0800
	ES_WANTRETURN  = 0x1000
	ES_NUMBER      = 0x2000
	BS_PUSHBUTTON  = 0x0000

	WM_DESTROY     = 0x0002
	WM_CLOSE       = 0x0010
	WM_SETFONT     = 0x0030
	WM_COMMAND     = 0x0111
	WM_APP         = 0x8000
	EM_SETSEL      = 0x00B1
	EM_REPLACESEL  = 0x00C2
	EM_SCROLLCARET = 0x00B7

	CW_USEDEFAULT = ^uintptr(0x7fffffff)
	MB_OK         = 0
	MB_ICONINFO   = 0x40
	MB_ICONWARN   = 0x30
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type point struct{ X, Y int32 }

type msgT struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	_       uint32
}

type actCtx struct {
	Size                  uint32
	Flags                 uint32
	Source                *uint16
	ProcessorArchitecture uint16
	LangId                uint16
	AssemblyDirectory     *uint16
	ResourceName          *uint16
	ApplicationName       *uint16
	Module                uintptr
}

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func sendMsg(h uintptr, m uint32, w, l uintptr) uintptr {
	r, _, _ := pSendMessageW.Call(h, uintptr(m), w, l)
	return r
}

func setText(h uintptr, s string) { pSetWindowTextW.Call(h, uintptr(unsafe.Pointer(u16(s)))) }

func getText(h uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(h)
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func enable(h uintptr, on bool) {
	v := uintptr(0)
	if on {
		v = 1
	}
	pEnableWindow.Call(h, v)
}

func msgBox(h uintptr, text, title string, flags uintptr) {
	pMessageBoxW.Call(h, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags)
}

func openURL(u string) {
	c := exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if c.Start() != nil {
		pShellExecuteW.Call(0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16(u))), 0, 0, 1)
	}
}

// Visual moderno (Common Controls v6) sem precisar de arquivo de recurso.
const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
<dependency><dependentAssembly><assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/></dependentAssembly></dependency>
<application xmlns="urn:schemas-microsoft-com:asm.v3"><windowsSettings><dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true</dpiAware></windowsSettings></application>
</assembly>`

func enableVisualStyles() {
	p := filepath.Join(os.TempDir(), "kicktimer-osircheese.manifest")
	if os.WriteFile(p, []byte(manifest), 0644) != nil {
		return
	}
	ac := actCtx{Source: u16(p)}
	ac.Size = uint32(unsafe.Sizeof(ac))
	h, _, _ := pCreateActCtxW.Call(uintptr(unsafe.Pointer(&ac)))
	if h != ^uintptr(0) {
		var cookie uintptr
		pActivateActCtx.Call(h, uintptr(unsafe.Pointer(&cookie)))
	}
	pInitCommonControls.Call()
}
