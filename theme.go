//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Tema preto/cinza com ouro #FFBF00 (mesma paleta do hub). COLORREF = 0x00BBGGRR.
const (
	colBg       = 0x00161616 // fundo da janela
	colField    = 0x00262626 // fundo dos campos
	colBtn      = 0x002E2E2E // botão secundário
	colBtnPress = 0x00242424
	colText     = 0x00E6E6E6 // texto principal
	colMuted    = 0x009A9A9A // rótulos
	colDisabled = 0x00707070
	colGold     = 0x0000BFFF // #FFBF00
	colGoldDark = 0x000099CC // ouro pressionado
)

var (
	uxtheme = syscall.NewLazyDLL("uxtheme.dll")
	dwmapi  = syscall.NewLazyDLL("dwmapi.dll")

	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	pCreatePen        = gdi32.NewProc("CreatePen")
	pSelectObject     = gdi32.NewProc("SelectObject")
	pRoundRect        = gdi32.NewProc("RoundRect")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
	pSetBkColor       = gdi32.NewProc("SetBkColor")
	pSetBkMode        = gdi32.NewProc("SetBkMode")
	pFillRect         = user32.NewProc("FillRect")
	pDrawTextW        = user32.NewProc("DrawTextW")
	pInvalidateRect   = user32.NewProc("InvalidateRect")
	pIsIconic         = user32.NewProc("IsIconic")
	pIsWindowEnabled  = user32.NewProc("IsWindowEnabled")
	pSetWindowTheme   = uxtheme.NewProc("SetWindowTheme")
	pDwmSetWindowAttr = dwmapi.NewProc("DwmSetWindowAttribute")

	pGetCurrentProcess        = kernel32.NewProc("GetCurrentProcess")
	pSetProcessWorkingSetSize = kernel32.NewProc("SetProcessWorkingSetSize")

	brBg, brField uintptr
)

const (
	WM_SIZE           = 0x0005
	WM_DRAWITEM       = 0x002B
	WM_CTLCOLOREDIT   = 0x0133
	WM_CTLCOLORSTATIC = 0x0138
	EM_SETREADONLY    = 0x00CF
	BS_OWNERDRAW      = 0x000B
	ODS_SELECTED      = 0x0001
	ODS_DISABLED      = 0x0004
	DT_CENTER         = 0x0001
	DT_VCENTER        = 0x0004
	DT_SINGLELINE     = 0x0020
	SIZE_MINIMIZED    = 1
	TRANSPARENT       = 1
)

type rect struct{ L, T, R, B int32 }

type drawItem struct {
	CtlType, CtlID, ItemID, ItemAction, ItemState uint32
	Hwnd, Hdc                                     uintptr
	Rc                                            rect
	ItemData                                      uintptr
}

func initTheme() {
	brBg, _, _ = pCreateSolidBrush.Call(colBg)
	brField, _, _ = pCreateSolidBrush.Call(colField)
}

// Barra de título escura (Win10/11) com fundo grafite e título em ouro (Win11).
func darkTitleBar(h uintptr) {
	if pDwmSetWindowAttr.Find() != nil {
		return
	}
	on := int32(1)
	pDwmSetWindowAttr.Call(h, 20, uintptr(unsafe.Pointer(&on)), 4)
	pDwmSetWindowAttr.Call(h, 19, uintptr(unsafe.Pointer(&on)), 4)
	cap, txt := uint32(colBg), uint32(colGold)
	pDwmSetWindowAttr.Call(h, 35, uintptr(unsafe.Pointer(&cap)), 4)
	pDwmSetWindowAttr.Call(h, 36, uintptr(unsafe.Pointer(&txt)), 4)
}

// Barras de rolagem escuras nos campos de várias linhas.
func darkScroll(h uintptr) {
	if pSetWindowTheme.Find() == nil {
		pSetWindowTheme.Call(h, uintptr(unsafe.Pointer(u16("DarkMode_Explorer"))), 0)
	}
}

func setReadOnly(h uintptr, ro bool) {
	v := uintptr(0)
	if ro {
		v = 1
	}
	sendMsg(h, EM_SETREADONLY, v, 0)
}

// Botões desenhados à mão: primário = ouro com texto preto; secundário = grafite com borda e texto em ouro.
func paintButton(d *drawItem, primary bool, font uintptr) {
	pressed := d.ItemState&ODS_SELECTED != 0
	disabled := d.ItemState&ODS_DISABLED != 0
	fill, border, text := uintptr(colBtn), uintptr(colGold), uintptr(colGold)
	if primary {
		fill, border, text = colGold, colGold, 0x00000000
		if pressed {
			fill, border = colGoldDark, colGoldDark
		}
	} else if pressed {
		fill = colBtnPress
	}
	if disabled {
		fill, border, text = colBtn, colBtn, colDisabled
	}
	pFillRect.Call(d.Hdc, uintptr(unsafe.Pointer(&d.Rc)), brBg)
	br, _, _ := pCreateSolidBrush.Call(fill)
	pen, _, _ := pCreatePen.Call(0, 1, border)
	oldBr, _, _ := pSelectObject.Call(d.Hdc, br)
	oldPen, _, _ := pSelectObject.Call(d.Hdc, pen)
	pRoundRect.Call(d.Hdc, uintptr(d.Rc.L), uintptr(d.Rc.T), uintptr(d.Rc.R), uintptr(d.Rc.B), 8, 8)
	pSelectObject.Call(d.Hdc, oldBr)
	pSelectObject.Call(d.Hdc, oldPen)
	deleteObj(br)
	deleteObj(pen)

	oldFont, _, _ := pSelectObject.Call(d.Hdc, font)
	pSetBkMode.Call(d.Hdc, TRANSPARENT)
	pSetTextColor.Call(d.Hdc, text)
	s := getText(d.Hwnd)
	p := u16(s)
	r := d.Rc
	pDrawTextW.Call(d.Hdc, uintptr(unsafe.Pointer(p)), ^uintptr(0), uintptr(unsafe.Pointer(&r)), DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	pSelectObject.Call(d.Hdc, oldFont)
}

var pDeleteObject = gdi32.NewProc("DeleteObject")

func deleteObj(h uintptr) { pDeleteObject.Call(h) }

// Devolve ao Windows a memória que não está em uso (chamado quando minimizado).
func trimMemory() {
	freeGoMemory()
	proc, _, _ := pGetCurrentProcess.Call()
	pSetProcessWorkingSetSize.Call(proc, ^uintptr(0), ^uintptr(0))
}

func isMinimized(h uintptr) bool {
	r, _, _ := pIsIconic.Call(h)
	return r != 0
}
