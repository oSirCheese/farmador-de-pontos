//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Paleta do balão (igual ao hub). COLORREF = 0x00BBGGRR.
const (
	cPanel     = 0x00242424
	cPanelLine = 0x003A3A3A
	cCard      = 0x002F2F2F
	cCardLine  = 0x003D3D3D
	cChip      = 0x003A3A3A
	cWhite     = 0x00FFFFFF
	cText      = 0x00E6E6E6
	cMuted     = 0x008F8F8F
	cDim       = 0x00606060
	cGold      = 0x0000BFFF // #FFBF00
	cGoldDark  = 0x000099CC
	cField     = 0x00262626
	cFieldLine = 0x00444444
	cRed       = 0x006B6BFF // #FF6B6B
	cBtn2      = 0x00383838
	cBtn2Press = 0x002C2C2C
)

var (
	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pGetTextExtentPoint32W  = gdi32.NewProc("GetTextExtentPoint32W")
	pEllipse                = gdi32.NewProc("Ellipse")
	pTextOutW               = gdi32.NewProc("TextOutW")
	pCreateRoundRectRgn     = gdi32.NewProc("CreateRoundRectRgn")
	pGetDeviceCaps          = gdi32.NewProc("GetDeviceCaps")
	pBeginPaint             = user32.NewProc("BeginPaint")
	pEndPaint               = user32.NewProc("EndPaint")
	pGetDC                  = user32.NewProc("GetDC")
	pReleaseDC              = user32.NewProc("ReleaseDC")
	pDrawIconEx             = user32.NewProc("DrawIconEx")
	pSetWindowRgn           = user32.NewProc("SetWindowRgn")
	pGetDpiForSystem        = user32.NewProc("GetDpiForSystem")

	brushes = map[uintptr]uintptr{}
	pens    = map[uintptr]uintptr{}
	dpi     = 96

	fTitle, fBold, fNormal, fSmall, fSmallBold, fLabel, fSym, fBtn uintptr
	iconCheese                                                     uintptr
)

type paintStruct struct {
	Hdc         uintptr
	Erase       int32
	Rc          rect
	Restore     int32
	IncUpdate   int32
	RGBReserved [32]byte
}

// S converte medidas de 96 DPI para a escala da tela.
func S(v int) int { return v * dpi / 96 }

func initGfx(inst uintptr) {
	if pGetDpiForSystem.Find() == nil {
		d, _, _ := pGetDpiForSystem.Call()
		if d > 0 {
			dpi = int(d)
		}
	} else {
		dc, _, _ := pGetDC.Call(0)
		d, _, _ := pGetDeviceCaps.Call(dc, 90) // LOGPIXELSY
		pReleaseDC.Call(0, dc)
		if d > 0 {
			dpi = int(d)
		}
	}
	face := "Segoe UI"
	fTitle = mkFont(face, 15, 700)
	fBold = mkFont(face, 13, 700)
	fNormal = mkFont(face, 13, 400)
	fSmall = mkFont(face, 12, 400)
	fSmallBold = mkFont(face, 12, 700)
	fLabel = mkFont(face, 11, 600)
	fBtn = mkFont(face, 12, 700)
	fSym = mkFont("Segoe UI Symbol", 13, 400)
	ic, _, _ := pLoadImageW.Call(inst, 1, 1, uintptr(S(24)), uintptr(S(24)), 0)
	iconCheese = ic
}

func mkFont(face string, px, weight int) uintptr {
	height := -S(px)
	h, _, _ := pCreateFontW.Call(uintptr(height), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(u16(face))))
	return h
}

func brush(c uintptr) uintptr {
	if b, ok := brushes[c]; ok {
		return b
	}
	b, _, _ := pCreateSolidBrush.Call(c)
	brushes[c] = b
	return b
}

func pen(c uintptr) uintptr {
	if p, ok := pens[c]; ok {
		return p
	}
	p, _, _ := pCreatePen.Call(0, uintptr(max(1, S(1))), c)
	pens[c] = p
	return p
}

// canvas: desenho com coordenadas em pixels já escalados.
type canvas struct{ dc uintptr }

func (c canvas) fill(x, y, w, h int, col uintptr) {
	r := rect{int32(x), int32(y), int32(x + w), int32(y + h)}
	pFillRect.Call(c.dc, uintptr(unsafe.Pointer(&r)), brush(col))
}

func (c canvas) round(x, y, w, h, rad int, fillC, lineC uintptr) {
	ob, _, _ := pSelectObject.Call(c.dc, brush(fillC))
	op, _, _ := pSelectObject.Call(c.dc, pen(lineC))
	pRoundRect.Call(c.dc, uintptr(x), uintptr(y), uintptr(x+w), uintptr(y+h), uintptr(rad), uintptr(rad))
	pSelectObject.Call(c.dc, ob)
	pSelectObject.Call(c.dc, op)
}

func (c canvas) dot(x, y, d int, col uintptr) {
	ob, _, _ := pSelectObject.Call(c.dc, brush(col))
	op, _, _ := pSelectObject.Call(c.dc, pen(col))
	pEllipse.Call(c.dc, uintptr(x), uintptr(y), uintptr(x+d), uintptr(y+d))
	pSelectObject.Call(c.dc, ob)
	pSelectObject.Call(c.dc, op)
}

func (c canvas) measure(s string, font uintptr) int {
	if s == "" {
		return 0
	}
	of, _, _ := pSelectObject.Call(c.dc, font)
	p, _ := syscall.UTF16FromString(s)
	var sz struct{ CX, CY int32 }
	pGetTextExtentPoint32W.Call(c.dc, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)-1), uintptr(unsafe.Pointer(&sz)))
	pSelectObject.Call(c.dc, of)
	return int(sz.CX)
}

// text desenha numa linha com centro vertical em cy; devolve a largura.
func (c canvas) text(x, cy int, s string, font, col uintptr) int {
	return c.textBox(x, cy-S(10), S(2000), S(20), s, font, col, 0x0004|0x0020|0x0800) // VCENTER|SINGLELINE|NOPREFIX
}

// textR alinha à direita terminando em right.
func (c canvas) textR(right, cy int, s string, font, col uintptr) int {
	w := c.measure(s, font)
	c.text(right-w, cy, s, font, col)
	return w
}

func (c canvas) textBox(x, y, w, h int, s string, font, col uintptr, flags uintptr) int {
	of, _, _ := pSelectObject.Call(c.dc, font)
	pSetBkMode.Call(c.dc, TRANSPARENT)
	pSetTextColor.Call(c.dc, col)
	r := rect{int32(x), int32(y), int32(x + w), int32(y + h)}
	p := u16(s)
	pDrawTextW.Call(c.dc, uintptr(unsafe.Pointer(p)), ^uintptr(0), uintptr(unsafe.Pointer(&r)), flags)
	pSelectObject.Call(c.dc, of)
	return c.measure(s, font)
}

func (c canvas) icon(x, y, size int) {
	if iconCheese != 0 {
		pDrawIconEx.Call(c.dc, uintptr(x), uintptr(y), iconCheese, uintptr(size), uintptr(size), 0, 0, 3)
	}
}

// button desenha um botão (primário = ouro; secundário = grafite com borda ouro).
func (c canvas) button(x, y, w, h int, label string, primary, pressed bool) {
	fillC, lineC, txt := uintptr(cBtn2), uintptr(cGold), uintptr(cGold)
	if pressed {
		fillC = cBtn2Press
	}
	if primary {
		fillC, lineC, txt = cGold, cGold, 0
		if pressed {
			fillC, lineC = cGoldDark, cGoldDark
		}
	}
	c.round(x, y, w, h, S(10), fillC, lineC)
	c.textBox(x, y, w, h, label, fBtn, txt, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
}

// doubleBuffer pinta sem piscar: fn desenha num bitmap e depois copia para a janela.
func doubleBuffer(h uintptr, w, ht int, fn func(c canvas)) {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(h, uintptr(unsafe.Pointer(&ps)))
	mdc, _, _ := pCreateCompatibleDC.Call(hdc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(ht))
	ob, _, _ := pSelectObject.Call(mdc, bmp)
	fn(canvas{mdc})
	pBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(ht), mdc, 0, 0, 0x00CC0020) // SRCCOPY
	pSelectObject.Call(mdc, ob)
	deleteObj(bmp)
	pDeleteDC.Call(mdc)
	pEndPaint.Call(h, uintptr(unsafe.Pointer(&ps)))
}

// floatingStyle: cantos arredondados (Win11 nativo; Win10 recorte), translúcido.
func floatingStyle(h uintptr, alpha byte) {
	pSetLayeredWindowAttributes.Call(h, 0, uintptr(alpha), 2) // LWA_ALPHA
	// recorte arredondado (funciona no Win10 e no Win11, mesmo com transparência)
	ok := false
	roundedByDWM[h] = ok
}

var (
	pSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	roundedByDWM                = map[uintptr]bool{}
)

// applyRegion recorta os cantos no Windows 10 (no 11 o próprio sistema arredonda).
func applyRegion(h uintptr, w, ht int) {
	if roundedByDWM[h] {
		return
	}
	rad := S(16)
	rg, _, _ := pCreateRoundRectRgn.Call(0, 0, uintptr(w+1), uintptr(ht+1), uintptr(rad), uintptr(rad))
	pSetWindowRgn.Call(h, rg, 1)
}
