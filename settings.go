//go:build windows

package main

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"
)

var (
	pSetWindowPos          = user32.NewProc("SetWindowPos")
	pReleaseCapture        = user32.NewProc("ReleaseCapture")
	pCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	pAppendMenuW           = user32.NewProc("AppendMenuW")
	pTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	pDestroyMenu           = user32.NewProc("DestroyMenu")
	pGetCursorPos          = user32.NewProc("GetCursorPos")
	pSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	pGetWindowRect         = user32.NewProc("GetWindowRect")
	pMonitorFromPoint      = user32.NewProc("MonitorFromPoint")
	pMonitorFromWindow     = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW       = user32.NewProc("GetMonitorInfoW")
	pSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")
	pSetTimer              = user32.NewProc("SetTimer")
	pSetFocus              = user32.NewProc("SetFocus")
	pGetFocus              = user32.NewProc("GetFocus")
	pGetStockObject        = gdi32.NewProc("GetStockObject")
	pSetWindowLongPtrW     = user32.NewProc("SetWindowLongPtrW")
	pScreenToClient        = user32.NewProc("ScreenToClient")
)

func getStock(i uintptr) uintptr { r, _, _ := pGetStockObject.Call(i); return r }

// ações dos cliques nas configurações
const (
	sClose = iota + 1
	sTab
	sImport
	sTwLogin
	sKickLogin
	sKickToggle
	sAuth
	sRand
	sSeq
	sPhrToggle
	sPhrDel
	sPhrAdd
	sQuit
	sUpdate
	sSave
	sTwWeb
	sTwToggle
)

const sW = 390

var (
	setW, setH              int
	setTab                  int // 0 Contas, 1 Canais, 2 Frases
	setHits                 []hitRect
	edClient, edSecret      uintptr
	edWatch, edTw, edChans  uintptr
	edNewPhr, edMin         uintptr
	loggingIn               bool
	lastLoginErr, twLastErr string
	phrScroll               int
	tabEdits                [3][]uintptr
)

const phrRows = 10

// alturas (em 96 DPI) de cada aba, sem o rodapé
func tabHeight(t int) int {
	switch t {
	case 0:
		if isAuthorized() {
			return 84 + 146 + 6 + 146 + 6 + 52 // já autorizada: cartão pequeno, sem botão nem campos
		}
		return 84 + 146 + 6 + 146 + 6 + 160
	case 1:
		return 84 + 3*(146+6) + 14
	}
	return 84 + 10 + 26 + 6 + phrRows*24 + 8 + 30 + 8 + 30 + 10
}

const footH = 136

var (
	weightHover          bool
	setTracking          bool
	weightTop, weightBot int
)

func openSettings() {
	if hwndSet != 0 {
		pSetForegroundWindow.Call(hwndSet)
		return
	}
	setW, setH = S(sW), S(tabHeight(setTab)+footH)
	var br rect
	pGetWindowRect.Call(hwndMain, uintptr(unsafe.Pointer(&br)))
	mon, _, _ := pMonitorFromWindow.Call(hwndMain, 2)
	mi := struct {
		Size      uint32
		Mon, Work rect
		Flags     uint32
	}{Size: 40}
	pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	x := int(br.L) - setW - S(10)
	if x < int(mi.Work.L) {
		x = int(br.R) + S(10)
	}
	if x+setW > int(mi.Work.R) {
		x = int(mi.Work.R) - setW
	}
	y := min(int(br.T), int(mi.Work.B)-S(tabHeight(0)+footH))
	y = max(y, int(mi.Work.T))

	hwndSet, _, _ = pCreateWindowExW.Call(0x00080000|0x00000008|0x00000080, // LAYERED|TOPMOST|TOOLWINDOW
		uintptr(unsafe.Pointer(u16("FarmadorConfig"))), uintptr(unsafe.Pointer(u16("Configurações"))),
		0x80000000, uintptr(x), uintptr(y), uintptr(setW), uintptr(setH), hwndMain, 0, hInst, 0)
	floatingStyle(hwndSet, 248)
	applyRegion(hwndSet, setW, setH)

	cfgMu.Lock()
	c := cfg
	cfgMu.Unlock()
	edit := func(x, y, w, h int, text string, multi bool) uintptr {
		style := uintptr(WS_CHILD | WS_TABSTOP)
		ex, ey, ew, eh := S(x+8), S(y)+(S(h)-S(18))/2, S(w-16), S(18)
		if multi {
			style |= ES_MULTILINE | ES_AUTOVSCROLL | ES_WANTRETURN | WS_VSCROLL
			ex, ey, ew, eh = S(x+8), S(y+6), S(w-10), S(h-12)
		} else {
			style |= ES_AUTOHSCROLL
		}
		hw, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(u16("EDIT"))), uintptr(unsafe.Pointer(u16(text))),
			style, uintptr(ex), uintptr(ey), uintptr(ew), uintptr(eh), hwndSet, 0, hInst, 0)
		sendMsg(hw, WM_SETFONT, fNormal, 1)
		if multi {
			darkScroll(hw)
		}
		return hw
	}
	cx, cw := sPadX()+10, sW-2*sPadX()-20
	// Contas: campos da autorização para digitar (último cartão)
	dY := 84 + 146 + 6 + 146 + 6
	edClient = edit(cx, dY+64, cw, 28, c.ClientID, false)
	edSecret = edit(cx, dY+114, cw, 28, c.ClientSecret, false)
	sendMsg(edSecret, 0x00CC, '•', 0)
	tabEdits[0] = []uintptr{edClient, edSecret}
	// Canais
	edWatch = edit(cx, 84+28, cw, 108, strings.Join(c.KickWatch, "\r\n"), true)
	edTw = edit(cx, 84+152+28, cw, 108, strings.Join(c.TwChannels, "\r\n"), true)
	edChans = edit(cx, 84+304+28, cw, 108, strings.Join(c.Channels, "\r\n"), true)
	tabEdits[1] = []uintptr{edWatch, edTw, edChans}
	// Frases
	fy := 84 + 10 + 26 + 6 + phrRows*24 + 8
	edNewPhr = edit(cx, fy, cw-100, 30, "", false)
	edMin = edit(cx+62, fy+38, 46, 30, strconv.Itoa(c.IntervalMin), false)
	pSetWindowLongPtrW.Call(edMin, ^uintptr(15), uintptr(WS_CHILD|WS_TABSTOP|ES_AUTOHSCROLL|ES_NUMBER))
	tabEdits[2] = []uintptr{edNewPhr, edMin}

	showTab(setTab)
	measureWeight()
	pSetTimer.Call(hwndSet, 7, 2000, 0) // peso do app
	pShowWindow.Call(hwndSet, 5)
	pSetForegroundWindow.Call(hwndSet)
}

func sPadX() int { return 8 }

func showTab(t int) {
	setTab = t
	for i, list := range tabEdits {
		for _, h := range list {
			if i == t && !(i == 0 && isAuthorized()) { // autorizada: Client ID/Secret ficam escondidos
				pShowWindow.Call(h, 5)
			} else {
				pShowWindow.Call(h, 0)
			}
		}
	}
	setW, setH = S(sW), S(tabHeight(t)+footH)
	pSetWindowPos.Call(hwndSet, 0, 0, 0, uintptr(setW), uintptr(setH), 0x0002|0x0004|0x0010)
	applyRegion(hwndSet, setW, setH)
	pInvalidateRect.Call(hwndSet, 0, 0)
}

func linesOf(h uintptr, twitch bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(getText(h), "\n") {
		s := parseSlug(l)
		if twitch {
			s = strings.TrimPrefix(s, "#")
		}
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// readSettings copia os campos para a configuração e salva. Devolve se a lista da Twitch mudou.
func readSettings() bool {
	if hwndSet == 0 {
		return false
	}
	tw := linesOf(edTw, true)
	cfgMu.Lock()
	cfg.ClientID = strings.TrimSpace(getText(edClient))
	cfg.ClientSecret = strings.TrimSpace(getText(edSecret))
	if n, err := strconv.Atoi(strings.TrimSpace(getText(edMin))); err == nil && n >= 1 {
		cfg.IntervalMin = n
	}
	cfg.Channels = linesOf(edChans, false)
	cfg.KickWatch = linesOf(edWatch, false)
	twChanged := strings.Join(tw, ",") != strings.Join(cfg.TwChannels, ",")
	cfg.TwChannels = tw
	cfgMu.Unlock()
	saveConfig()
	return twChanged
}

func closeSettings() {
	twChanged := readSettings()
	pDestroyWindow.Call(hwndSet)
	hwndSet = 0
	if twChanged {
		twApply()
	}
	wake()
	refreshMain()
}

func isAuthorized() bool {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return cfg.RefreshToken != "" && !loggingIn
}

func settingsLoginDone(ok bool) {
	showTab(setTab) // autorizou: o cartão encolhe e os campos somem
	pInvalidateRect.Call(hwndSet, 0, 0)
	if !ok && lastLoginErr != "" {
		msgBox(hwndSet, "A autorização não deu certo:\n\n"+lastLoginErr+"\n\nConfira o Client ID, o Client Secret e se a Redirect URL do app da Kick é:\n"+redirectURI, appName, MB_ICONWARN)
	}
}

func addPhrase() {
	t := strings.TrimSpace(getText(edNewPhr))
	if t == "" {
		return
	}
	cfgMu.Lock()
	cfg.Phrases = append(cfg.Phrases, Phrase{t, true})
	n := len(cfg.Phrases)
	cfgMu.Unlock()
	setText(edNewPhr, "")
	phrScroll = max(0, n-phrRows)
	saveConfig()
	pInvalidateRect.Call(hwndSet, 0, 0)
	refreshMain()
}

// ---------- desenho ----------

func paintSettings(c canvas) {
	W := setW
	setHits = setHits[:0]
	hit := func(x, y, w, h, id, n int) { setHits = append(setHits, hitRect{x: x, y: y, w: w, h: h, id: id, n: n}) }
	btn := func(x, y, w, h int, label string, primary bool, id, n int) {
		c.button(x, y, w, h, label, primary, false)
		hit(x, y, w, h, id, n)
	}
	c.fill(0, 0, W, setH, cPanel)
	pad := S(sPadX())
	cy := S(8 + 17)
	c.icon(pad+S(6), cy-S(12), S(24))
	c.text(pad+S(36), cy, "Configurações", fTitle, cWhite)
	xb := W - pad - S(6) - S(26)
	c.round(xb, cy-S(11), S(26), S(22), S(10), cChip, cChip)
	c.textBox(xb, cy-S(11), S(26), S(22), "✕", fSym, cWhite, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	hit(xb, cy-S(11), S(26), S(22), sClose, 0)

	// abas
	tx := pad
	for i, name := range []string{"Contas", "Canais", "Frases"} {
		w := c.measure(name, fBtn) + S(26)
		if i == setTab {
			c.round(tx, S(46), w, S(28), S(12), cGold, cGold)
			c.textBox(tx, S(46), w, S(28), name, fBtn, 0, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
		} else {
			c.round(tx, S(46), w, S(28), S(12), cCard, cCardLine)
			c.textBox(tx, S(46), w, S(28), name, fSmall, cText, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
		}
		hit(tx, S(46), w, S(28), sTab, i)
		tx += w + S(6)
	}

	cw := W - 2*pad
	lx := pad + S(10)
	right := pad + cw - S(10)
	cardAt := func(y, h int) { c.round(pad, S(y), cw, S(h), S(16), cCard, cCardLine) }
	field := func(x, y, w, h int) { c.round(S(x), S(y), S(w), S(h), S(12), cField, cFieldLine) }
	fcx, fcw := sPadX()+10, sW-2*sPadX()-20

	cfgMu.Lock()
	cc := cfg
	phr := append([]Phrase(nil), cfg.Phrases...)
	cfgMu.Unlock()

	switch setTab {
	case 0: // ----- Contas -----
		y := 84
		cardAt(y, 146)
		c.text(lx, S(y+18), "TWITCH · ASSISTIR", fLabel, cMuted)
		// para saber quais lives estão ao vivo, basta um acesso oficial da Twitch (qualquer conta)
		hasTok := false
		for _, t := range cc.TwAccs {
			if t.Access != "" {
				hasTok = true
			}
		}
		twMu.Lock()
		if twLiveErr == "auth" {
			hasTok = false // há acesso guardado, mas nenhum funciona
		}
		twMu.Unlock()
		switch {
		case twLogging[0]:
			c.textR(right, S(y+18), "autorize no navegador…", fSmall, cMuted)
		case !hasTok:
			btn(right-S(150), S(y+7), S(150), S(22), "Autorizar (lives ao vivo)", true, sTwLogin, 0)
		}
		for i := 0; i < 4; i++ {
			ry := S(y + 44 + i*26)
			a := cc.TwAccs[i]
			hit(lx, ry-S(10), S(200), S(20), sTwWeb, i) // clicar no nome abre o login desta conta
			xx := lx + c.text(lx, ry, fmt.Sprintf("%02d", i+1), fBold, cGold) + S(8)
			name := a.Conta
			if name == "" {
				name = a.Login
			}
			if name == "" {
				name = fmt.Sprintf("Conta %02d", i+1)
			}
			xx += c.text(xx, ry, name, fBold, cWhite) + S(6)
			if a.User != "" && !strings.EqualFold(a.User, a.Conta) {
				c.text(xx, ry, a.User, fSmall, cMuted)
			}
			switch {
			case !webviewOK:
				c.textR(right, ry, "falta o WebView2", fSmall, cMuted)
			case a.User == "":
				btn(right-S(116), ry-S(11), S(116), S(22), "Entrar na Twitch", false, sTwWeb, i)
			case i == 0:
				w := c.measure("sempre ligada", fSmallBold) + S(16)
				c.round(right-w, ry-S(10), w, S(20), S(10), cGold, cGold)
				c.textBox(right-w, ry-S(10), w, S(20), "sempre ligada", fSmallBold, 0, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
			default:
				lbl, fill, col := "Pausada", uintptr(cChip), uintptr(cText)
				if a.Watch {
					lbl, fill, col = "Assistindo", cGold, 0
				}
				w := c.measure(lbl, fSmallBold) + S(16)
				c.round(right-w, ry-S(10), w, S(20), S(10), fill, fill)
				c.textBox(right-w, ry-S(10), w, S(20), lbl, fSmallBold, col, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
				hit(right-w, ry-S(10), w, S(20), sTwToggle, i)
			}
		}

		y += 146 + 6
		cardAt(y, 146)
		c.text(lx, S(y+18), "KICK · ASSISTIR", fLabel, cMuted)
		for i := 0; i < 4; i++ {
			ry := S(y + 44 + i*26)
			a := cc.KickAccs[i]
			hit(lx, ry-S(10), S(200), S(20), sKickLogin, i) // clicar no nome abre o login desta conta
			xx := lx + c.text(lx, ry, fmt.Sprintf("%02d", i+1), fBold, cGold) + S(8)
			name := a.Conta
			if name == "" {
				name = a.User
			}
			if name == "" {
				name = fmt.Sprintf("Conta %02d", i+1)
			}
			xx += c.text(xx, ry, name, fBold, cWhite) + S(6)
			if i == 0 {
				c.text(xx, ry, "digita também", fSmall, cMuted)
			} else if a.User != "" && a.User != a.Conta {
				c.text(xx, ry, a.User, fSmall, cMuted)
			}
			switch {
			case !webviewOK:
				c.textR(right, ry, "falta o WebView2", fSmall, cMuted)
			case a.User == "":
				btn(right-S(104), ry-S(11), S(104), S(22), "Entrar na Kick", false, sKickLogin, i)
			case i == 0:
				w := c.measure("sempre ligada", fSmallBold) + S(16)
				c.round(right-w, ry-S(10), w, S(20), S(10), cGold, cGold)
				c.textBox(right-w, ry-S(10), w, S(20), "sempre ligada", fSmallBold, 0, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
			default:
				lbl, fill, col := "Pausada", uintptr(cChip), uintptr(cText)
				if a.Watch {
					lbl, fill, col = "Assistindo", cGold, 0
				}
				w := c.measure(lbl, fSmallBold) + S(16)
				c.round(right-w, ry-S(10), w, S(20), S(10), fill, fill)
				c.textBox(right-w, ry-S(10), w, S(20), lbl, fSmallBold, col, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
				hit(right-w, ry-S(10), w, S(20), sKickToggle, i)
			}
		}

		y += 146 + 6
		if cc.RefreshToken != "" && !loggingIn { // já autorizada: só mostra quem digita
			cardAt(y, 46)
			c.text(lx, S(y+23), "DIGITAR", fLabel, cMuted)
			who := cc.Username
			if who == "" {
				who = "oSirCheese"
			}
			w := c.textR(right, S(y+23), "autorizada", fSmall, cGold)
			c.textR(right-w-S(8), S(y+23), who, fBold, cGold)
			break
		}
		cardAt(y, 154)
		c.text(lx, S(y+18), "DIGITAR · AUTORIZAÇÃO", fLabel, cMuted)
		c.textR(right, S(y+18), "conta que digita", fSmall, cMuted)
		switch {
		case loggingIn:
			c.text(lx, S(y+42), "Autorize no navegador…", fSmall, cMuted)
		case cc.Username != "":
			c.text(lx, S(y+42), cc.Username, fBold, cGold)
		case cc.RefreshToken != "":
			c.text(lx, S(y+42), "Autorizada", fBold, cGold)
		default:
			c.text(lx, S(y+42), "Não autorizada", fNormal, cMuted)
		}
		if !loggingIn {
			btn(right-S(90), S(y+30), S(90), S(24), "Autorizar", false, sAuth, 0)
		}
		c.text(lx, S(y+60), "Client ID", fSmall, cMuted) // campos ficam em y+64 e y+114
		field(fcx, y+64, fcw, 28)
		c.text(lx, S(y+108)-S(1), "Client Secret", fSmall, cMuted)
		field(fcx, y+114, fcw, 28)

	case 1: // ----- Canais -----
		titles := []struct{ t, r string }{
			{"KICK · ASSISTIR", fmt.Sprintf("%d canais", len(cc.KickWatch))},
			{"TWITCH · FICAR NO CHAT", fmt.Sprintf("%d canais", len(cc.TwChannels))},
			{"KICK · DIGITAR", "só streamers que permitiram"},
		}
		for i, t := range titles {
			y := 84 + i*152
			cardAt(y, 146)
			w := c.text(lx, S(y+16), t.t, fLabel, cMuted)
			if i == 2 {
				c.text(lx+w+S(6), S(y+16), "oSirCheese", fSmallBold, cGold)
			} else {
				c.text(lx+w+S(6), S(y+16), "lista oficial", fSmall, cGold)
			}
			c.textR(right, S(y+16), t.r, fSmall, cMuted)
			field(fcx, y+28, fcw, 108)
		}
		seen, _ := offcStatus()
		if seen == "" {
			seen = "lista oficial: abra o hub (conta 01) para receber"
		}
		c.textBox(pad, S(84+3*152)-S(2), cw, S(16), seen+" · atualiza sozinho", fSmall, cDim, DT_CENTER|DT_VCENTER|DT_SINGLELINE)

	case 2: // ----- Frases -----
		y := 84
		cardAt(y, tabHeight(2)-84)
		w := c.text(lx, S(y+23), "FRASES", fLabel, cMuted)
		c.text(lx+w+S(8), S(y+23), "marque as que usar", fSmall, cMuted)
		sw := c.measure("Em sequência", fBtn) + S(20)
		rw := c.measure("Sorteada", fBtn) + S(20)
		btn(right-sw, S(y+10), sw, S(26), "Em sequência", cc.Order == "sequencia", sSeq, 0)
		btn(right-sw-S(6)-rw, S(y+10), rw, S(26), "Sorteada", cc.Order != "sequencia", sRand, 0)
		ly := y + 10 + 26 + 6
		phrScroll = max(0, min(phrScroll, len(phr)-phrRows))
		if len(phr) == 0 {
			c.text(lx, S(ly+12), "Nenhuma frase. Escreva uma abaixo e clique em Adicionar.", fSmall, cMuted)
		}
		for r := 0; r < phrRows && phrScroll+r < len(phr); r++ {
			i := phrScroll + r
			p := phr[i]
			ry := S(ly + r*24)
			cb := S(14)
			if p.On {
				c.round(lx, ry+S(5), cb, cb, S(5), cGold, cGold)
				c.textBox(lx, ry+S(5), cb, cb, "✓", fSym, 0, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
			} else {
				c.round(lx, ry+S(5), cb, cb, S(5), cField, cGold)
			}
			hit(lx-S(2), ry, cw-S(50), S(24), sPhrToggle, i)
			col := uintptr(cText)
			if !p.On {
				col = cMuted
			}
			c.textBox(lx+S(22), ry, cw-S(64), S(24), p.Text, fSmall, col, 0x0004|0x0020|0x8000|0x0800)
			c.textBox(right-S(20), ry, S(20), S(24), "✕", fSym, cMuted, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
			hit(right-S(22), ry, S(24), S(24), sPhrDel, i)
			if r < phrRows-1 && i < len(phr)-1 {
				c.fill(lx, ry+S(24)-1, cw-S(20), 1, 0x00353535)
			}
		}
		if len(phr) > phrRows {
			c.textR(right, S(ly+phrRows*24)-S(2), fmt.Sprintf("%d–%d de %d · role com o mouse", phrScroll+1, min(len(phr), phrScroll+phrRows), len(phr)), fSmall, cDim)
		}
		fy := ly + phrRows*24 + 8
		field(fcx, fy, fcw-100, 30)
		btn(right-S(92), S(fy), S(92), S(30), "Adicionar", false, sPhrAdd, 0)
		c.text(lx, S(fy+38+15), "Intervalo", fSmall, cMuted)
		field(fcx+62, fy+38, 46, 30)
		c.text(S(fcx+62+46+8), S(fy+38+15), "min · só com a live ligada", fSmall, cMuted)
	}

	// peso do app (atualiza a cada 2 s)
	wy := tabHeight(setTab) + 6
	cardAt(wy, 54)
	c.text(lx, S(wy+17), "PESO AGORA", fLabel, cMuted)
	c.text(lx+S(82), S(wy+17), wText, fSmallBold, cGold)
	c.textBox(lx, S(wy+29), cw-S(20), S(18), wTextLine+"  · passe o mouse para detalhes", fSmall, cMuted, 0x0004|0x0020|0x8000|0x0800)
	weightTop, weightBot = S(wy), S(wy+54)
	if weightHover && len(wDetails) > 0 { // quadro com tudo escrito, por cima da aba
		lh := S(18)
		bh := lh*len(wDetails) + S(16)
		by := S(wy) - bh - S(4)
		c.round(pad, by, cw, bh, S(14), cCard, cGold)
		for i, l := range wDetails {
			col := uintptr(cText)
			if i == 0 {
				col = cGold
			}
			c.textBox(lx, by+S(8)+i*lh, cw-S(20), lh, l, fSmall, col, 0x0004|0x0020|0x0800)
		}
	}

	// rodapé
	fy := S(wy + 54 + 8)
	btn(pad, fy, S(136), S(30), "Fechar o Farmador", false, sQuit, 0)
	if v, _ := updFoundInfo(); v != "" {
		btn(pad+S(142), fy, S(128), S(30), "Atualizar para v"+v, true, sUpdate, 1)
	} else {
		btn(pad+S(142), fy, S(128), S(30), "Atualizar por zip…", false, sUpdate, 0)
	}
	btn(W-pad-S(90), fy, S(90), S(30), "Salvar", true, sSave, 0)
	c.textBox(0, fy+S(36), W, S(20), "Desenvolvido por oSirCheese · v"+appVersion, fSmall, cDim, DT_CENTER|DT_VCENTER|DT_SINGLELINE)

	ob, _, _ := pSelectObject.Call(c.dc, getStock(5))
	op, _, _ := pSelectObject.Call(c.dc, pen(cPanelLine))
	pRoundRect.Call(c.dc, 0, 0, uintptr(W), uintptr(setH), uintptr(S(16)), uintptr(S(16)))
	pSelectObject.Call(c.dc, ob)
	pSelectObject.Call(c.dc, op)
}

func settingsClick(r hitRect) {
	switch r.id {
	case sClose, sSave:
		closeSettings()
	case sTab:
		readSettings()
		showTab(r.n)
	case sQuit:
		quitApp()
	case sTwLogin:
		readSettings()
		i := r.n
		twLogging[i] = true
		go func() {
			err := twDeviceLogin(i)
			twLogging[i] = false
			ok := uintptr(1)
			if err != nil {
				twLastErr, ok = err.Error(), 0
			} else {
				twPollLive() // conta as lives na hora, sem esperar o próximo ciclo
				pPostMessageW.Call(hwndMain, wmKickSync, 0, 0)
			}
			pPostMessageW.Call(hwndMain, wmTwDone, ok, 0)
		}()
	case sKickLogin:
		kickOpenWindow("kick", r.n, "")
	case sTwWeb:
		kickOpenWindow("tw", r.n, "")
	case sTwToggle:
		cfgMu.Lock()
		cfg.TwAccs[r.n].Watch = !cfg.TwAccs[r.n].Watch
		cfgMu.Unlock()
		saveConfig()
		pPostMessageW.Call(hwndMain, wmKickSync, 0, 0)
	case sKickToggle:
		cfgMu.Lock()
		cfg.KickAccs[r.n].Watch = !cfg.KickAccs[r.n].Watch
		cfgMu.Unlock()
		saveConfig()
		pPostMessageW.Call(hwndMain, wmKickSync, 0, 0)
	case sAuth:
		readSettings()
		cfgMu.Lock()
		ok := cfg.ClientID != "" && cfg.ClientSecret != ""
		cfgMu.Unlock()
		if !ok {
			msgBox(hwndSet, "Preencha o Client ID e o Client Secret do seu app da Kick (kick.com/settings/developer).\n\nRedirect URL do app: "+redirectURI, appName, MB_ICONWARN)
			return
		}
		loggingIn = true
		go func() {
			ok := uintptr(1)
			if err := doLogin(); err != nil {
				lastLoginErr, ok = err.Error(), 0
			}
			pPostMessageW.Call(hwndMain, wmLoginDone, ok, 0)
		}()
	case sRand, sSeq:
		cfgMu.Lock()
		if r.id == sSeq {
			cfg.Order = "sequencia"
		} else {
			cfg.Order = "sorteada"
		}
		cfgMu.Unlock()
		saveConfig()
	case sPhrToggle:
		cfgMu.Lock()
		if r.n < len(cfg.Phrases) {
			cfg.Phrases[r.n].On = !cfg.Phrases[r.n].On
		}
		cfgMu.Unlock()
		saveConfig()
	case sPhrDel:
		cfgMu.Lock()
		if r.n < len(cfg.Phrases) {
			cfg.Phrases = append(cfg.Phrases[:r.n], cfg.Phrases[r.n+1:]...)
		}
		cfgMu.Unlock()
		saveConfig()
	case sPhrAdd:
		addPhrase()
	case sUpdate:
		if r.n == 1 { // zip achado em Downloads / Área de Trabalho
			applyFoundUpdate(hwndSet)
			return
		}
		p := openFileDialog(hwndSet, "Escolha o zip da nova versão do Farmador", "Zip do Farmador (*.zip)", "*.zip")
		if p == "" {
			return
		}
		readSettings()
		if err := updateFromZip(p); err != nil {
			msgBox(hwndSet, "Não deu para atualizar: "+err.Error(), appName, MB_ICONWARN)
			return
		}
		quitApp()
		return
	}
	if hwndSet != 0 {
		pInvalidateRect.Call(hwndSet, 0, 0)
	}
	refreshMain()
}

func settingsProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case 0x0014:
		return 1
	case 0x000F:
		doubleBuffer(h, setW, setH, paintSettings)
		return 0
	case WM_CTLCOLOREDIT, WM_CTLCOLORSTATIC:
		pSetTextColor.Call(w, cText)
		pSetBkColor.Call(w, cField)
		return brush(cField)
	case 0x0201: // WM_LBUTTONDOWN
		x, y := int(int16(l&0xffff)), int(int16((l>>16)&0xffff))
		for i := len(setHits) - 1; i >= 0; i-- { // os últimos desenhados ficam por cima
			r := setHits[i]
			if x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h {
				settingsClick(r)
				return 0
			}
		}
		pReleaseCapture.Call()
		sendMsg(h, 0x00A1, 2, 0)
		return 0
	case 0x0200: // WM_MOUSEMOVE: detalhes do peso ao passar o mouse
		if !setTracking {
			tme := struct {
				Size, Flags uint32
				Hwnd        uintptr
				Hover       uint32
			}{Flags: 2, Hwnd: h}
			tme.Size = uint32(unsafe.Sizeof(tme))
			pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			setTracking = true
		}
		y := int(int16((l >> 16) & 0xffff))
		in := y >= weightTop && y < weightBot
		if in != weightHover {
			weightHover = in
			tabEditsVisible(!in) // os campos ficariam por cima do quadro de detalhes
			pInvalidateRect.Call(h, 0, 0)
		}
		return 0
	case 0x02A3: // WM_MOUSELEAVE
		setTracking = false
		if weightHover {
			weightHover = false
			tabEditsVisible(true)
			pInvalidateRect.Call(h, 0, 0)
		}
		return 0
	case 0x0113: // WM_TIMER: peso do app
		if w == 7 {
			measureWeight()
			pInvalidateRect.Call(h, 0, 0)
		}
		return 0
	case 0x020A: // WM_MOUSEWHEEL: rola a lista de frases
		if setTab == 2 {
			d := int(int16(w >> 16))
			if d < 0 {
				phrScroll++
			} else {
				phrScroll--
			}
			phrScroll = max(0, phrScroll)
			pInvalidateRect.Call(h, 0, 0)
		}
		return 0
	case WM_COMMAND:
		if int(w&0xffff) == 1 { // Enter
			if f, _, _ := pGetFocus.Call(); f == edNewPhr {
				addPhrase()
			}
		}
		return 0
	case WM_CLOSE:
		closeSettings()
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(h, uintptr(m), w, l)
	return r
}

func tabEditsVisible(on bool) {
	for _, hw := range tabEdits[setTab] {
		if on && !(setTab == 0 && isAuthorized()) {
			pShowWindow.Call(hw, 5)
		} else {
			pShowWindow.Call(hw, 0)
		}
	}
}
