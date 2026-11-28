//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	appName    = "Farmador de pontos"
	appVersion = "3.4.6"
	defaultMin = 30
)

const (
	wmRefresh   = WM_APP + 1
	wmLoginDone = WM_APP + 2
	wmKickSync  = WM_APP + 3
	wmTwDone    = WM_APP + 4
	wmOffc      = WM_APP + 5
	wmCheckDone = WM_APP + 6
	wmCheckPlan = WM_APP + 7
	wmAutoUpdate = WM_APP + 8
)

const (
	hitNone = iota
	hitCollapse
	hitGear
	hitStart
	hitKickSel
	hitKickChan
	hitKickToggle
	hitUpdate
)

var (
	kickHover        bool // mouse em cima do cartão da Kick: mostra canais e pontos
	kickTop, kickBot int
	twHover          bool // idem para a Twitch
	twTop, twBot     int
	mouseTracking    bool
)

var (
	hwndMain, hwndSet, hInst uintptr
	curW, curH               int
	hits                     []hitRect
)

type hitRect struct {
	x, y, w, h, id int
	n              int
	s              string
}

// ---------- configuração ----------

func cfgPath() string {
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "kick-timer.json")
}

func loadConfig() {
	cfg = Config{IntervalMin: defaultMin, Order: "sorteada"}
	if b, err := os.ReadFile(cfgPath()); err == nil {
		json.Unmarshal(b, &cfg)
	}
	if len(cfg.Messages) == 0 && len(cfg.Phrases) == 0 {
		if m := strings.TrimSpace(cfg.Message); m != "" {
			cfg.Messages = []string{m}
		} else {
			cfg.Messages = []string{"Ronaldo brilha muito no Corinthians"}
		}
	}
	if cfg.CfgVer < 16 && cfg.IntervalMin == 20 { // 20 era só o padrão antigo
		cfg.IntervalMin = defaultMin
	}
	if cfg.IntervalMin < 1 {
		cfg.IntervalMin = defaultMin
	}
	if cfg.Order != "sequencia" {
		cfg.Order = "sorteada"
	}
	migrateV2()
	cfg.CfgVer = 20
	saveConfig()
}

func saveConfig() {
	cfgMu.Lock()
	// mantém o campo antigo compatível
	cfg.Messages = cfg.Messages[:0]
	for _, p := range cfg.Phrases {
		cfg.Messages = append(cfg.Messages, p.Text)
	}
	if len(cfg.Messages) > 0 {
		cfg.Message = cfg.Messages[0]
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	cfgMu.Unlock()
	os.WriteFile(cfgPath(), b, 0600)
}

func parseSlug(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	for _, p := range []string{"https://", "http://", "www.", "kick.com/", "twitch.tv/"} {
		s = strings.TrimPrefix(s, p)
	}
	if i := strings.IndexAny(s, "/?# "); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "@")
}

// ---------- textos ----------

// ptsColor: cor dos pontos de um canal (200–499 vermelho, 500–999 azul, 1000+ amarelo).
// Abaixo de 200 não tem cor (ok = false).
func ptsColor(p int) (col uintptr, ok bool) {
	switch {
	case p >= 1000:
		return cPts1000, true
	case p >= 500:
		return cPts500, true
	case p >= 200:
		return cPts200, true
	}
	return cMuted, false
}

func fmtPts(n int) string { // 1560 → 1.560
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	out := ""
	for len(s) > 3 {
		out = "." + s[len(s)-3:] + out
		s = s[:len(s)-3]
	}
	return s + out
}

func fmtAgo(t time.Time) string {
	if t.IsZero() {
		return "ao vivo"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "ao vivo agora"
	case d < time.Hour:
		return fmt.Sprintf("ao vivo há %dmin", int(d.Minutes()))
	default:
		return fmt.Sprintf("ao vivo há %dh%02d", int(d.Hours()), int(d.Minutes())%60)
	}
}

func nextLabel(s snapshot, st chanState) (string, bool) { // texto, é horário (ouro)?
	if !s.Sending {
		return "pausado", false
	}
	if st.LastSent.IsZero() {
		return "agora", true
	}
	n := st.LastSent.Add(s.Interval)
	if !n.After(time.Now()) {
		return "agora", true
	}
	return n.Format("15:04"), true
}

func liveList(s snapshot) (live []string, offline []string, errs []string) {
	for _, c := range s.Channels {
		st := s.States[c]
		switch {
		case st.Err != "" && !st.Live:
			errs = append(errs, c)
		case st.Live:
			live = append(live, c)
		default:
			offline = append(offline, c)
		}
	}
	return
}

func kickWatching(s snapshot, i int) bool {
	return s.KickAccs[i].Watch && s.KickAccs[i].User != ""
}

// fresh: o valor foi conferido há menos de 90 min (senão pode estar velho, ex.: já resgatado)
func fresh(a KickAcc, ch string) bool {
	t, ok := a.PtsAt[ch]
	return ok && time.Since(time.Unix(t, 0)) < 90*time.Minute
}

// platView: o que o cartão de assistir precisa, igual para Kick e Twitch
type platView struct {
	plat, name string
	chans      []string
	live       map[string]bool
	nlive      int
	accs       [nAccs]KickAcc
	sel        int
	hover      bool
}

func kickView(s snapshot) platView {
	return platView{"kick", "Kick", s.KickWatch, s.KickLive, len(s.KickLive), s.KickAccs, s.KickSel, kickHover}
}

func twitchView(s snapshot) platView {
	var accs [nAccs]KickAcc
	for i, a := range s.TwAccsW {
		accs[i] = KickAcc{Conta: a.Conta, Watch: a.Watch, User: a.User, Pts: a.Pts, PtsAt: a.PtsAt}
	}
	return platView{"tw", "Twitch", s.TwWatch, s.TwLiveSet, s.TwLive, accs, s.TwSel, twHover}
}

func pvWatching(pv platView, i int) bool {
	return pv.accs[i].Watch && pv.accs[i].User != ""
}

func platWatching(pv platView) int {
	n := 0
	for i := range pv.accs {
		if pvWatching(pv, i) {
			n++
		}
	}
	return n
}

func platHot(pv platView) int {
	n := 0
	for _, c := range pv.chans {
		if p, ok := pv.accs[pv.sel].Pts[c]; ok && p >= 200 && fresh(pv.accs[pv.sel], c) {
			n++
		}
	}
	return n
}

func kickHot(s snapshot) int { // canais com 200+ na conta selecionada
	n := 0
	for _, c := range s.KickWatch {
		if p, ok := s.KickAccs[s.KickSel].Pts[c]; ok && p >= 200 && fresh(s.KickAccs[s.KickSel], c) {
			n++
		}
	}
	return n
}

func twOnline(s snapshot) int {
	n := 0
	for _, t := range s.TwStat {
		if t.State == "no chat" {
			n++
		}
	}
	return n
}

// ---------- balão principal ----------

func render(c canvas, s snapshot, collapsed bool) int {
	W := curW
	pad := S(8)
	hits = hits[:0]
	c.fill(0, 0, W, S(3000), cPanel)
	live, offline, errs := liveList(s)
	nWatch := 0
	for i := range s.KickAccs {
		if kickWatching(s, i) {
			nWatch++
		}
	}

	// cabeçalho
	hy, hh := S(8), S(34)
	cy := hy + hh/2
	c.icon(pad+S(6), cy-S(12), S(24))
	x := pad + S(36)
	x += c.text(x, cy, "Farmador de pontos", fTitle, cWhite) + S(8)
	// selinhos K (Kick, verde) e T (Twitch, roxo): letra simples no selinho do app
	for _, b := range []struct {
		l   string
		col uintptr
	}{{"K", 0x005AC346}, {"T", 0x00E06B9B}} { // #46C35A e #9B6BE0
		bw := S(18)
		c.round(x, cy-S(9), bw, S(18), S(8), cChip, b.col)
		c.textBox(x, cy-S(9), bw, S(18), b.l, fSmallBold, b.col, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
		x += bw + S(4)
	}
	if !collapsed {
		c.text(x+S(4), cy+S(1), "v"+appVersion, fSmall, cMuted)
	}
	gx := W - pad - S(6) - S(26)
	c.round(gx, cy-S(11), S(26), S(22), S(10), cChip, cChip)
	c.textBox(gx, cy-S(11), S(26), S(22), "⚙", fSym, cWhite, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	hits = append(hits, hitRect{x: gx, y: cy - S(11), w: S(26), h: S(22), id: hitGear})
	ax := gx - S(28)
	arrow := "▼"
	if collapsed {
		arrow = "▲"
	}
	c.textBox(ax, cy-S(11), S(24), S(22), arrow, fSym, cMuted, DT_CENTER|DT_VCENTER|DT_SINGLELINE)
	hits = append(hits, hitRect{x: ax, y: cy - S(11), w: S(24), h: S(22), id: hitCollapse})
	y := hy + hh
	if v, where := updFoundInfo(); v != "" {
		msg := "Nova versão v" + v + " em " + where + " · clique para atualizar"
		c.round(pad, y, W-2*pad, S(24), S(12), cChip, cGold)
		c.textBox(pad, y, W-2*pad, S(24), msg, fSmallBold, cGold, DT_CENTER|DT_VCENTER|DT_SINGLELINE|0x0800)
		hits = append(hits, hitRect{x: pad, y: y, w: W - 2*pad, h: S(24), id: hitUpdate})
		y += S(28)
	}

	if collapsed {
		row := func(name, mid string, right func(r int, cy int)) {
			ry := y + S(10)
			c.text(pad+S(6), ry, name, fSmallBold, cWhite)
			c.text(pad+S(60), ry, mid, fSmall, cText)
			right(W-pad-S(6), ry)
			y += S(20)
		}
		for _, pv := range []platView{twitchView(s), kickView(s)} {
			pv := pv
			row(pv.name, fmt.Sprintf("%d ao vivo", pv.nlive), func(r, cy int) {
				w := c.textR(r, cy, fmt.Sprintf("%d com 200+", platHot(pv)), fSmallBold, cGold)
				c.textR(r-w, cy, fmt.Sprintf("%d assistindo · ", platWatching(pv)), fSmall, cMuted)
			})
		}
		if _, note := offcStatus(); note != "" {
			c.textBox(pad+S(6), y, W-2*pad-S(12), S(20), note, fSmall, cGold, 0x0004|0x0020|0x8000|0x0800)
			y += S(20)
		}
		c.fill(pad+S(6), y+S(4), W-2*pad-S(12), max(1, S(1)), cCardLine)
		y += S(8)
		if len(live) == 0 {
			msg := "Nenhuma live para digitar agora"
			if s.GlobalErr != "" {
				msg = s.GlobalErr
			}
			c.textBox(pad+S(6), y, W-2*pad-S(12), S(20), msg, fSmall, cMuted, 0x0004|0x0020|0x8000)
			y += S(20)
		}
		for _, name := range live {
			ry := y + S(10)
			c.dot(pad+S(6), ry-S(3), S(7), cGold)
			c.text(pad+S(20), ry, name, fSmallBold, cWhite)
			nl, gold := nextLabel(s, s.States[name])
			col := uintptr(cMuted)
			if gold {
				col = cGold
			}
			w := c.textR(W-pad-S(6), ry, nl, fSmallBold, col)
			if gold {
				c.textR(W-pad-S(6)-w-S(6), ry, "próxima", fSmall, cMuted)
			}
			y += S(20)
		}
		return y + S(8)
	}

	cw := W - 2*pad
	card := func(h int) int { // desenha um cartão e devolve o topo
		y += S(6)
		top := y
		c.round(pad, top, cw, h, S(16), cCard, cCardLine)
		y += h
		return top
	}
	right := pad + cw - S(10)
	lx := pad + S(10)

	// --- cartão de assistir (Kick ou Twitch): mesma lógica nas duas ---
	drawPlat := func(pv platView) {
		// lista de canais: ao vivo primeiro, depois offline
		klist := append([]string(nil), pv.chans...)
		sort.SliceStable(klist, func(a, b int) bool { return pv.live[klist[a]] && !pv.live[klist[b]] })
		sel := pv.sel
		// mede a altura da lista
		type item struct {
			txt  string
			slug string
			x, y int
			live bool
			hot  bool // 200+: ganha a cor da faixa (negrito só se ao vivo)
			col  uintptr
			pts  string
		}
		var items []item
		ix, iy := 0, 0
		maxW := cw - S(20)
		for _, ch := range klist {
			p, ok := pv.accs[sel].Pts[ch]
			ps := ""
			if ok {
				ps = fmtPts(p)
			}
			dotc := "○ "
			if pv.live[ch] {
				dotc = "● "
			}
			w := c.measure(dotc+ch, fSmall)
			if ps != "" {
				w += S(4) + c.measure(ps, fSmall)
			}
			if ix > 0 && ix+w > maxW {
				ix, iy = 0, iy+S(18)
			}
			pc, colored := ptsColor(p)
			items = append(items, item{dotc + ch, ch, ix, iy, pv.live[ch], ok && colored, pc, ps})
			ix += w + S(10)
		}
		listH := 0
		if len(items) > 0 {
			listH = iy + S(18)
		}
		kh := S(54) // fechado: título e conta
		if pv.hover {
			kh = S(54) + listH
			if len(items) > 0 {
				kh += S(42)
			}
		}
		t := card(kh)
		if pv.plat == "tw" {
			twTop, twBot = t, t+kh
		} else {
			kickTop, kickBot = t, t+kh
		}
		l1 := t + S(17)
		xx := lx + c.text(lx, l1, pv.name, fBold, cWhite) + S(8)
		total := 0
		for _, a := range pv.accs {
			for _, ch := range pv.chans {
				total += a.Pts[ch]
			}
		}
		xx += c.text(xx, l1, fmt.Sprintf("%d ao vivo · %d canais · ", pv.nlive, len(pv.chans)), fNormal, cText)
		c.text(xx, l1, fmtPts(total)+" pts", fBold, cGold)
		status := "assistindo"
		var statusCol uintptr = cMuted
		switch {
		case !webviewOK:
			status = "falta o WebView2"
		case pv.plat == "tw" && s.TwLiveErr == "auth":
			status, statusCol = "autorize a Twitch", cGold
			hits = append(hits, hitRect{x: right - S(130), y: l1 - S(10), w: S(130), h: S(20), id: hitGear})
		case pv.plat == "tw" && s.TwLiveErr == "rede":
			status = "sem conexão com a Twitch"
		case pv.accs[sel].User == "":
			status = "sem login"
		case platWatching(pv) == 0:
			status = "pausada"
		}
		if !pv.hover && len(items) > 0 {
			aw := c.textR(right, l1, "▾", fSym, cMuted)
			c.textR(right-aw-S(4), l1, status, fSmall, statusCol)
		} else {
			c.textR(right, l1, status, fSmall, statusCol)
		}

		l2 := t + S(40)
		xx = lx + c.text(lx, l2, "CONTA", fLabel, cMuted) + S(8)
		acc := pv.accs[sel]
		accName := acc.Conta
		if accName == "" {
			accName = acc.User
		}
		if accName == "" {
			accName = fmt.Sprintf("Conta %02d", sel+1)
		}
		c.text(xx, l2, accName, fSmallBold, cText)
		if acc.User == "" {
			c.textR(right, l2, "sem login", fSmall, cMuted)
		} else { // ligar / pausar a conta (assistir as lives)
			lbl, primary := "Pausar", false
			if !acc.Watch {
				lbl, primary = "Ligar", true
			}
			bw := c.measure(lbl, fBtn) + S(22)
			c.button(right-bw, l2-S(11), bw, S(22), lbl, primary, false)
			hits = append(hits, hitRect{x: right - bw, y: l2 - S(11), w: bw, h: S(22), id: hitKickToggle, n: sel, s: pv.plat})
		}

		if pv.hover && len(items) > 0 {
			base := t + S(54)
			for _, it := range items {
				col := uintptr(cMuted)
				if it.live {
					col = cText
				}
				font := fSmall
				if it.hot {
					col = it.col
					if it.live {
						font = fSmallBold
					}
				}
				iy := base + it.y + S(9)
				w := c.text(lx+it.x, iy, it.txt, font, col)
				if it.pts != "" {
					pc := uintptr(cMuted)
					if it.hot {
						pc = it.col
					}
					w += S(4) + c.text(lx+it.x+w+S(4), iy, it.pts, font, pc)
				}
				hits = append(hits, hitRect{x: lx + it.x, y: base + it.y, w: w, h: S(18), id: hitKickChan, n: sel, s: pv.plat + "|" + it.slug})
			}
			c.text(lx, base+listH+S(11), "Vermelho 200+ · Azul 500+ · Amarelo 1000+ · negrito = ao vivo", fSmall, cMuted)
			c.text(lx, base+listH+S(29), "Clique para abrir e resgatar", fSmall, cMuted)
		}
	}
	drawPlat(twitchView(s))
	drawPlat(kickView(s))

	if _, note := offcStatus(); note != "" {
		y += S(4)
		c.textBox(pad+S(4), y, cw-S(8), S(34), note, fSmall, cGold, 0x0010|0x0800)
		y += S(16) * (1 + min(1, c.measure(note, fSmall)/max(1, cw-S(8))))
	}

	// --- Digitar ---
	t := card(S(54))
	l1 := t + S(17)
	xx := lx + c.text(lx, l1, "Digitar", fBold, cWhite) + S(8)
	who := s.Username
	if who == "" {
		who = "oSirCheese"
	}
	c.text(xx, l1, who, fNormal, cMuted)
	bw, bh := S(72), S(24)
	label := "Iniciar"
	if s.Sending {
		label = "Parar"
	}
	c.button(right-bw, t+S(6), bw, bh, label, !s.Sending, false)
	hits = append(hits, hitRect{x: right - bw, y: t + S(6), w: bw, h: bh, id: hitStart})
	l2 := t + S(39)
	if s.GlobalErr != "" {
		c.textBox(lx, l2-S(10), cw-S(20), S(20), s.GlobalErr, fSmall, cGold, 0x0004|0x0020|0x8000)
	} else {
		dc, st := uintptr(cDim), "Parado"
		if s.Sending {
			dc, st = cGold, "Rodando"
		}
		c.dot(lx, l2-S(3), S(7), dc)
		xx = lx + S(13)
		xx += c.text(xx, l2, st, fSmall, cText)
		modo := "sorteadas"
		if s.Order == "sequencia" {
			modo = "em sequência"
		}
		xx += c.text(xx, l2, fmt.Sprintf(" · %d de %d frases · %s · a cada ", s.PhrOn, s.PhrAll, modo), fSmall, cMuted)
		c.text(xx, l2, fmt.Sprintf("%d min", int(s.Interval.Minutes())), fSmallBold, cGold)
	}

	// --- digitando agora ---
	if len(live) > 0 {
		y += S(8)
		c.text(pad+S(4), y+S(8), fmt.Sprintf("DIGITANDO AGORA · %d AO VIVO", len(live)), fLabel, cMuted)
		y += S(16)
		for i, name := range live {
			st := s.States[name]
			t := card(S(54))
			l1 := t + S(17)
			xx := lx + c.text(lx, l1, fmt.Sprintf("%02d", i+1), fBold, cGold) + S(8)
			c.text(xx, l1, name, fBold, cWhite)
			ago := fmtAgo(st.Since)
			aw := c.textR(right, l1, ago, fSmall, cMuted)
			c.dot(right-aw-S(11), l1-S(3), S(7), cGold)
			l2 := t + S(38)
			xx = lx + c.text(lx, l2, "Última", fSmall, cMuted) + S(6)
			last := "—"
			if !st.LastSent.IsZero() {
				last = st.LastSent.Format("15:04")
			}
			xx += c.text(xx, l2, last, fSmall, cText) + S(8)
			nl, gold := nextLabel(s, st)
			col := uintptr(cMuted)
			if gold {
				col = cGold
			}
			nw := c.textR(right, l2, nl, fSmallBold, col)
			if gold {
				nw += S(6) + c.textR(right-nw-S(6), l2, "próxima", fSmall, cMuted)
			}
			if st.LastMsg != "" {
				mw := right - nw - S(10) - xx
				tw := min(c.measure("\""+st.LastMsg+"\"", fSmall)+S(16), mw)
				if tw > S(30) {
					c.round(xx, l2-S(10), tw, S(20), S(10), cChip, cChip)
					c.textBox(xx+S(8), l2-S(10), tw-S(16), S(20), "\""+st.LastMsg+"\"", fSmall, cText, 0x0004|0x0020|0x8000|0x0800)
				}
			}
		}
	}
	if len(offline) > 0 {
		txt := "Offline: "
		for i, n := range offline {
			add := n
			if i > 0 {
				add = " · " + n
			}
			if len([]rune(txt+add)) > 110 {
				txt += fmt.Sprintf(" · +%d", len(offline)-i)
				break
			}
			txt += add
		}
		y += S(6)
		c.textBox(pad+S(4), y, cw-S(8), S(34), txt, fSmall, cMuted, 0x0010|0x0800)
		y += S(16) * (1 + min(1, c.measure(txt, fSmall)/max(1, cw-S(8))))
	}
	for i, n := range errs {
		if i == 3 {
			c.text(pad+S(4), y+S(9), fmt.Sprintf("+%d com erro", len(errs)-3), fSmall, cRed)
			y += S(18)
			break
		}
		c.textBox(pad+S(4), y, cw-S(8), S(18), n+": "+s.States[n].Err, fSmall, cRed, 0x0004|0x0020|0x8000|0x0800)
		y += S(18)
	}
	return y + S(10)
}

func computeHeight() int {
	s := takeSnapshot()
	cfgMu.Lock()
	col := cfg.Collapsed
	cfgMu.Unlock()
	dc, _, _ := pGetDC.Call(0)
	mdc, _, _ := pCreateCompatibleDC.Call(dc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(dc, 1, 1)
	ob, _, _ := pSelectObject.Call(mdc, bmp)
	h := render(canvas{mdc}, s, col)
	pSelectObject.Call(mdc, ob)
	deleteObj(bmp)
	pDeleteDC.Call(mdc)
	pReleaseDC.Call(0, dc)
	return h
}

func refreshMain() {
	h := computeHeight()
	if h != curH {
		curH = h
		pSetWindowPos.Call(hwndMain, 0, 0, 0, uintptr(curW), uintptr(curH), 0x0002|0x0004|0x0010)
		applyRegion(hwndMain, curW, curH)
	}
	pInvalidateRect.Call(hwndMain, 0, 0)
}

func toggleSending() {
	s := takeSnapshot()
	if !s.Sending {
		cfgMu.Lock()
		ready := cfg.RefreshToken != "" && len(cfg.Channels) > 0
		cfgMu.Unlock()
		if !ready || len(activePhrasesLocked()) == 0 {
			openSettings()
			return
		}
	}
	sending.Store(!s.Sending)
	cfgMu.Lock()
	cfg.Sending = !s.Sending
	cfgMu.Unlock()
	saveConfig()
	wake()
	refreshMain()
}

func activePhrasesLocked() []string {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return activePhrases()
}

func mainProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case 0x0014: // WM_ERASEBKGND
		return 1
	case 0x000F: // WM_PAINT
		s := takeSnapshot()
		cfgMu.Lock()
		col := cfg.Collapsed
		cfgMu.Unlock()
		doubleBuffer(h, curW, curH, func(c canvas) {
			render(c, s, col)
			ob, _, _ := pSelectObject.Call(c.dc, getStock(5)) // NULL_BRUSH
			op, _, _ := pSelectObject.Call(c.dc, pen(cPanelLine))
			pRoundRect.Call(c.dc, 0, 0, uintptr(curW), uintptr(curH), uintptr(S(16)), uintptr(S(16)))
			pSelectObject.Call(c.dc, ob)
			pSelectObject.Call(c.dc, op)
		})
		return 0
	case 0x0200: // WM_MOUSEMOVE: abre/fecha a lista da Kick
		if !mouseTracking {
			tme := struct {
				Size, Flags uint32
				Hwnd        uintptr
				Hover       uint32
			}{Flags: 2, Hwnd: h} // TME_LEAVE
			tme.Size = uint32(unsafe.Sizeof(tme))
			pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			mouseTracking = true
		}
		y := int(int16((l >> 16) & 0xffff))
		cfgMu.Lock()
		col := cfg.Collapsed
		cfgMu.Unlock()
		in := !col && y >= kickTop && y < kickBot
		inT := !col && y >= twTop && y < twBot
		if in != kickHover || inT != twHover {
			kickHover, twHover = in, inT
			refreshMain()
		}
		return 0
	case 0x02A3: // WM_MOUSELEAVE
		mouseTracking = false
		if kickHover || twHover {
			kickHover, twHover = false, false
			refreshMain()
		}
		return 0
	case 0x0201: // WM_LBUTTONDOWN
		x, y := int(int16(l&0xffff)), int(int16((l>>16)&0xffff))
		for _, r := range hits {
			if x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h {
				switch r.id {
				case hitCollapse:
					cfgMu.Lock()
					cfg.Collapsed = !cfg.Collapsed
					cfgMu.Unlock()
					saveConfig()
					refreshMain()
				case hitGear: // abre e fecha as configurações
					if hwndSet != 0 {
						closeSettings()
					} else {
						openSettings()
					}
				case hitStart:
					toggleSending()
				case hitKickToggle:
					cfgMu.Lock()
					if r.s == "tw" {
						cfg.TwAccs[r.n].Watch = !cfg.TwAccs[r.n].Watch
					} else {
						cfg.KickAccs[r.n].Watch = !cfg.KickAccs[r.n].Watch
					}
					cfgMu.Unlock()
					saveConfig()
					pPostMessageW.Call(hwndMain, wmKickSync, 0, 0)
					refreshMain()
				case hitKickChan:
					plat, slug, _ := strings.Cut(r.s, "|")
					kickOpenWindow(plat, r.n, slug)
				case hitUpdate:
					applyFoundUpdate(hwndMain)
				}
				return 0
			}
		}
		pReleaseCapture.Call()
		sendMsg(h, 0x00A1, 2, 0) // arrastar
		return 0
	case 0x0205: // WM_RBUTTONUP → menu
		menu, _, _ := pCreatePopupMenu.Call()
		pAppendMenuW.Call(menu, 0, 1, uintptr(unsafe.Pointer(u16("Configurações"))))
		pAppendMenuW.Call(menu, 0x800, 0, 0)
		pAppendMenuW.Call(menu, 0, 2, uintptr(unsafe.Pointer(u16("Fechar o Farmador"))))
		var pt point
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		pSetForegroundWindow.Call(h)
		cmd, _, _ := pTrackPopupMenu.Call(menu, 0x0100|0x0002, uintptr(pt.X), uintptr(pt.Y), 0, h, 0)
		pDestroyMenu.Call(menu)
		switch cmd {
		case 1:
			openSettings()
		case 2:
			quitApp()
		}
		return 0
	case 0x0232: // WM_EXITSIZEMOVE
		var r rect
		pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
		cfgMu.Lock()
		cfg.X, cfg.Y, cfg.HasPos = int(r.L), int(r.T), true
		cfgMu.Unlock()
		saveConfig()
		return 0
	case 0x0113: // WM_TIMER
		switch w {
		case 1:
			refreshMain()
		case 2:
			kickOpenNext()
		case 4:
			kickReloadOldest()
		case 5:
			checkStep()
			kickWatchdog()
		case 6:
			checkPlan()
		case 7:
			pollTick()
		case 3:
			cfgMu.Lock()
			d := kDirty
			kDirty = false
			cfgMu.Unlock()
			if d {
				saveConfig()
			}
		}
		return 0
	case wmRefresh:
		refreshMain()
		return 0
	case wmOffc:
		offcApply()
		return 0
	case wmCheckDone:
		checkDone()
		refreshMain()
		return 0
	case wmCheckPlan:
		checkPlan()
		return 0
	case wmAutoUpdate:
		if !quitting {
			applyFoundUpdate(hwndMain)
		}
		return 0
	case wmKickSync:
		kickSync()
		refreshMain()
		return 0
	case wmLoginDone:
		loggingIn = false
		if hwndSet != 0 {
			settingsLoginDone(w != 0)
		}
		wake()
		refreshMain()
		return 0
	case wmTwDone:
		if hwndSet != 0 {
			pInvalidateRect.Call(hwndSet, 0, 0)
		}
		if w == 0 && twLastErr != "" {
			msgBox(hwndSet, "Twitch: "+twLastErr, appName, MB_ICONWARN)
		}
		twApply()
		return 0
	case WM_CLOSE:
		quitApp()
		return 0
	case WM_DESTROY:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(h, uintptr(m), w, l)
	return r
}

func quitApp() {
	quitting = true
	if hwndSet != 0 {
		readSettings()
		pDestroyWindow.Call(hwndSet)
		hwndSet = 0
	}
	kickCloseAll()
	saveConfig()
	pDestroyWindow.Call(hwndMain)
}

func startPos(w int) (int, int) {
	cfgMu.Lock()
	x, y, has := cfg.X, cfg.Y, cfg.HasPos
	cfgMu.Unlock()
	if has {
		pt := uintptr(uint32(int32(x+S(20)))) | uintptr(uint32(int32(y+S(10))))<<32
		if mon, _, _ := pMonitorFromPoint.Call(pt, 0); mon != 0 {
			return x, y
		}
	}
	var wa rect
	pSystemParametersInfoW.Call(0x0030, 0, uintptr(unsafe.Pointer(&wa)), 0)
	return int(wa.R) - w - S(20), int(wa.T) + S(20)
}

func main() {
	runtime.LockOSThread()
	runtime.GOMAXPROCS(2)
	debug.SetGCPercent(50)
	fullSpeed()
	if !singleInstance() {
		return
	}
	cleanupUpdate()
	enableVisualStyles()
	initTheme()
	loadConfig()

	hInst, _, _ = pGetModuleHandleW.Call(0)
	initGfx(hInst)
	cursor, _, _ := pLoadCursorW.Call(0, 32512)
	icon, _, _ := pLoadImageW.Call(hInst, 1, 1, 0, 0, 0x40)
	iconSm, _, _ := pLoadImageW.Call(hInst, 1, 1, 16, 16, 0)
	if icon == 0 {
		icon, _, _ = pLoadIconW.Call(0, 32512)
		iconSm = icon
	}
	reg := func(name string, proc uintptr) *uint16 {
		cls := u16(name)
		wc := wndClassEx{WndProc: proc, Instance: hInst, Cursor: cursor, Icon: icon, IconSm: iconSm, Background: brush(cPanel), ClassName: cls}
		wc.Size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		return cls
	}
	clsMain := reg("FarmadorBalao", syscall.NewCallback(mainProc))
	reg("FarmadorConfig", syscall.NewCallback(settingsProc))
	kickInit()

	curW = S(370)
	curH = S(120)
	x, y := startPos(curW)
	hwndMain, _, _ = pCreateWindowExW.Call(0x00080000|0x00000008|0x00040000, // LAYERED|TOPMOST|APPWINDOW
		uintptr(unsafe.Pointer(clsMain)), uintptr(unsafe.Pointer(u16(appName+" v"+appVersion))),
		0x80000000|0x00020000, // WS_POPUP|WS_MINIMIZEBOX
		uintptr(x), uintptr(y), uintptr(curW), uintptr(curH), 0, 0, hInst, 0)
	floatingStyle(hwndMain, 242)

	// Digitar sempre começa parado: só liga quando você clicar em Iniciar.
	cfgMu.Lock()
	cfg.Sending = false
	cfgMu.Unlock()

	onUpdate = func() { pPostMessageW.Call(hwndMain, wmRefresh, 0, 0) }
	refreshMain()
	pShowWindow.Call(hwndMain, 4)
	pSetTimer.Call(hwndMain, 1, 30000, 0)                                                     // "ao vivo há…"
	pSetTimer.Call(hwndMain, 2, 1500, 0)                                                      // abre as páginas da Kick uma a uma
	pSetTimer.Call(hwndMain, 3, 60000, 0)                                                     // salva os pontos lidos
	pSetTimer.Call(hwndMain, 4, 60000, 0)                                                     // recarrega 1 página da Kick por minuto, se ela tiver 30+ min
	pSetTimer.Call(hwndMain, 5, 3000, 0)                                                      // conferência de pontos: um canal por vez
	pSetTimer.Call(hwndMain, 6, 30*60*1000, 0)                                                // conferência de pontos: nova rodada a cada 30 min
	pSetTimer.Call(hwndMain, 7, 15000, 0)                                                     // saldo da Kick direto do servidor (kickpoll.go)
	time.AfterFunc(2*time.Minute, func() { pPostMessageW.Call(hwndMain, wmCheckPlan, 0, 0) }) // primeira rodada logo depois de abrir
	go monitor()
	twApply()

	var m msgT
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if hwndSet != 0 {
			if d, _, _ := pIsDialogMessageW.Call(hwndSet, uintptr(unsafe.Pointer(&m))); d != 0 {
				continue
			}
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
