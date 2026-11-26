//go:build windows

package main

// Páginas reais da Kick (motor do Edge / WebView2) para as contas ficarem presentes
// nas lives, igual ao hub: uma sessão por conta, só lives ao vivo, escondidas,
// sem som e na menor qualidade, sem corte de desempenho em segundo plano.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"github.com/jchv/go-webview2/webviewloader"
)

const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

// Script das páginas escondidas: mantém "visível", sem som, menor qualidade,
// aceita avisos de idade, lê os pontos do canal e quem está logado.
const kickWatchJS = `(() => {
 if (window.__farm) return; window.__farm = 1;
 try { Object.defineProperty(document, 'hidden', { get: () => false }); Object.defineProperty(document, 'visibilityState', { get: () => 'visible' });
   document.addEventListener('visibilitychange', e => e.stopImmediatePropagation(), true); } catch (e) {}
 const post = o => { try { window.chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} };
 const num = t => { const m = /(\d{3,4})\s*p/i.exec(t || ''); return m ? +m[1] : 0; };
 let low = false, tries = 0;
 function lowest() {
   if (low || ++tries > 12) return;
   const gear = document.querySelector('[aria-label*="uality" i],[aria-label*="ualidade" i],[aria-label*="ettings" i],[aria-label*="onfigura" i],[data-testid*="settings" i],[data-testid*="quality" i]');
   if (!gear) return; try { gear.click(); } catch (e) {}
   setTimeout(() => {
     const items = [...document.querySelectorAll('[role="menuitem"],[role="menuitemradio"],[role="option"],li,button,div')].filter(e => e.children.length < 4 && num(e.textContent) && (e.textContent || '').length < 24);
     if (!items.length) return;
     items.sort((a, b) => num(a.textContent) - num(b.textContent)); try { items[0].click(); } catch (e) {} low = true;
     setTimeout(() => { try { document.body.click(); } catch (e) {} }, 300);
   }, 400);
 }
 function tick() {
   const v = document.querySelector('video');
   for (const b of document.querySelectorAll('button')) { const t = (b.innerText || '').trim(); if (/^(start watching|come[çc]ar a assistir|assistir|i am 18|tenho 18|sou maior|accept|aceitar|continue|continuar)\b/i.test(t)) { try { b.click(); } catch (e) {} break; } }
   if (v) { v.muted = true; v.volume = 0; if (v.paused) { try { v.play().catch(() => {}); } catch (e) {} } if (!low) lowest(); }
   const st = document.querySelector('[data-testid="channel-points-value"]'); let n = null;
   if (st) { const s = ((st.querySelector('[title]') || st).getAttribute('title') || st.textContent || '').trim().replace(/[.,\s]/g, ''); if (/^\d+$/.test(s)) n = parseInt(s, 10); }
   post({ t: 'pts', n });
 }
 const sess = () => { const m = /(?:^|;\s*)session_token=([^;]+)/.exec(document.cookie); return m ? decodeURIComponent(m[1]) : ''; };
 const loginBtn = () => [...document.querySelectorAll('button,a')].some(b => { const t = (b.innerText || '').trim(); const r = b.getBoundingClientRect(); return r.width > 0 && r.height > 0 && /^(log ?in|entrar|sign ?up|cadastr|registr)/i.test(t); });
 const pageReady = () => document.readyState === 'complete' && document.querySelectorAll('button').length > 5;
 async function who() { // logado = cookie de sessão da Kick (ou, se ele estiver escondido, nenhum botão "Entrar" na página)
   const t = sess();
   if (!t) { if (!pageReady()) { setTimeout(who, 10000); return; } post({ t: 'user', name: loginBtn() ? '' : 'conectada' }); return; }
   let name = 'conectada';
   try { const r = await fetch('/api/v1/user', { credentials: 'include', headers: { Accept: 'application/json', Authorization: 'Bearer ' + t } }); if (r.ok) { const j = await r.json(); name = (j && (j.username || j.slug)) || name; } } catch (e) {}
   post({ t: 'user', name }); }
 const loaded = () => post({ t: 'loaded' });
 if (document.readyState !== 'loading') loaded(); else document.addEventListener('DOMContentLoaded', loaded);
 setInterval(tick, 60000); setTimeout(tick, 15000); setInterval(() => { if (!low && document.querySelector('video')) lowest(); }, 4000);
 setTimeout(who, 10000); setInterval(who, 30 * 60000);
})();`

// Kick, todas as páginas: escuta as respostas do servidor que falam de pontos.
// O número desenhado na tela pode vir de um valor guardado (velho); a resposta da
// rede é o saldo de agora. Só vale a resposta do canal da própria página.
const kickNetJS = `(() => {
 if (window.__farmNet) return; window.__farmNet = 1;
 const post = o => { try { window.chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} };
 const slug = () => (location.pathname.split('/')[1] || '').toLowerCase();
 const num = v => typeof v === 'number' && isFinite(v) ? Math.floor(v) : (typeof v === 'string' && /^\d+$/.test(v) ? parseInt(v, 10) : null);
 const pick = j => { if (!j || typeof j !== 'object') return null; const d = (j.data && typeof j.data === 'object' && !Array.isArray(j.data)) ? j.data : j;
   for (const k of ['points', 'balance', 'points_balance', 'channel_points']) { const n = num(d[k]); if (n != null) return n; } return null; };
 const seen = (u, j) => { u = String(u || ''); if (!/point/i.test(u)) return; if (typeof j === 'string') { try { j = JSON.parse(j); } catch (e) { return; } }
   const s = slug(), n = pick(j), mine = !!s && new RegExp('/' + s.replace(/[^a-z0-9_-]/g, '') + '(/|\\?|$)', 'i').test(u);
   post({ t: 'ptsurl', u: u.slice(0, 200), n, mine });
   if (n != null && mine) post({ t: 'ptsnet', n }); };
 const of = window.fetch;
 if (of) window.fetch = function (...a) { const p = of.apply(this, a);
   try { const u = String((a[0] && a[0].url) || a[0] || ''); if (/point/i.test(u)) p.then(r => r.clone().text()).then(t => seen(u, t)).catch(() => {}); } catch (e) {}
   return p; };
 const oo = XMLHttpRequest.prototype.open;
 XMLHttpRequest.prototype.open = function (m, u, ...r) {
   try { if (/point/i.test(String(u))) this.addEventListener('load', () => { try { seen(u, this.responseType === 'json' ? this.response : this.responseText); } catch (e) {} }); } catch (e) {}
   return oo.call(this, m, u, ...r); };
})();`

// Script das janelas visíveis (login / resgatar): só avisa quem está logado.
const kickLoginJS = `(() => {
 if (window.__farmL) return; window.__farmL = 1;
 const post = o => { try { window.chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} };
 const sess = () => { const m = /(?:^|;\s*)session_token=([^;]+)/.exec(document.cookie); return m ? decodeURIComponent(m[1]) : ''; };
 const loginBtn = () => [...document.querySelectorAll('button,a')].some(b => { const t = (b.innerText || '').trim(); const r = b.getBoundingClientRect(); return r.width > 0 && r.height > 0 && /^(log ?in|entrar|sign ?up|cadastr|registr)/i.test(t); });
 const pageReady = () => document.readyState === 'complete' && document.querySelectorAll('button').length > 5;
 let done = false, noBtn = 0;
 async function check() { // só olha o cookie (sem pedir nada à Kick); quando aparece, busca o nome uma vez
   if (done) return; const t = sess();
   if (!t) { if (pageReady() && !loginBtn()) { if (++noBtn >= 2) { done = true; post({ t: 'user', name: 'conectada' }); } } else noBtn = 0; return; }
   done = true;
   let name = 'conectada';
   try { const r = await fetch('/api/v1/user', { credentials: 'include', headers: { Accept: 'application/json', Authorization: 'Bearer ' + t } }); if (r.ok) { const j = await r.json(); name = (j && (j.username || j.slug)) || name; } } catch (e) {}
   post({ t: 'user', name }); }
 setInterval(check, 3000); setTimeout(check, 1500);
 { const loaded = () => post({ t: 'loaded' }); if (document.readyState !== 'loading') loaded(); else document.addEventListener('DOMContentLoaded', loaded); }
 function pts() { const st = document.querySelector('[data-testid="channel-points-value"]'); if (!st) return; const s = ((st.querySelector('[title]') || st).getAttribute('title') || st.textContent || '').trim().replace(/[.,\s]/g, ''); if (/^\d+$/.test(s)) post({ t: 'pts', n: parseInt(s, 10) }); }
 setInterval(pts, 10000); setTimeout(pts, 6000); // depois de resgatar, o valor novo já vale
})();`

// Twitch, página escondida: menor qualidade e sem som (preferências do próprio player),
// "visível", aceita aviso de conteúdo, lê os pontos do canal e quem está logado.
const twWatchJS = `(() => {
 if (window.__farmT) return; window.__farmT = 1;
 try { localStorage.setItem('video-quality', JSON.stringify({ default: '160p30' })); localStorage.setItem('video-muted', JSON.stringify({ default: true })); localStorage.setItem('volume', '0'); } catch (e) {}
 try { Object.defineProperty(document, 'hidden', { get: () => false }); Object.defineProperty(document, 'visibilityState', { get: () => 'visible' });
   document.addEventListener('visibilitychange', e => e.stopImmediatePropagation(), true); } catch (e) {}
 const post = o => { try { window.chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} };
 const parsePts = t => { t = (t || '').trim().toLowerCase().replace(/\s/g, ''); const m = /^([\d.,]+)(k|mil|mi|m)?$/.exec(t); if (!m) return null;
   if (m[2]) { const mult = (m[2] === 'k' || m[2] === 'mil') ? 1e3 : 1e6; return Math.round(parseFloat(m[1].replace(',', '.')) * mult); }
   return parseInt(m[1].replace(/[.,]/g, ''), 10); };
 const pts = () => { const el = document.querySelector('[data-test-selector="copo-balance-string"]'); return el ? parsePts(el.textContent) : null; };
 function tick() {
   for (const sel of ['[data-a-target="content-classification-gate-overlay-start-watching-button"]', '[data-a-target="player-overlay-mature-accept"]']) { const b = document.querySelector(sel); if (b) { try { b.click(); } catch (e) {} } }
   const v = document.querySelector('video'); if (v) { v.muted = true; v.volume = 0; if (v.paused) { try { v.play().catch(() => {}); } catch (e) {} } }
   post({ t: 'pts', n: pts() });
 }
 const loginBtn = () => !!document.querySelector('[data-a-target="login-button"]');
 const pageReady = () => document.readyState === 'complete' && document.querySelectorAll('button').length > 5;
 function who() { const m = /(?:^|;\s*)login=([^;]+)/.exec(document.cookie);
   if (m) { post({ t: 'user', name: decodeURIComponent(m[1]) }); return; }
   if (!pageReady()) { setTimeout(who, 10000); return; }
   post({ t: 'user', name: loginBtn() ? '' : 'conectada' }); }
 const loaded = () => post({ t: 'loaded' });
 if (document.readyState !== 'loading') loaded(); else document.addEventListener('DOMContentLoaded', loaded);
 setInterval(tick, 60000); setTimeout(tick, 15000); setTimeout(who, 10000); setInterval(who, 30 * 60000);
})();`

// Twitch, janelas visíveis (entrar / resgatar): reconhece o login e lê os pontos.
const twLoginJS = `(() => {
 if (window.__farmTL) return; window.__farmTL = 1;
 const post = o => { try { window.chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} };
 let done = false;
 function check() { if (done) return; const m = /(?:^|;\s*)login=([^;]+)/.exec(document.cookie); if (m) { done = true; post({ t: 'user', name: decodeURIComponent(m[1]) }); } }
 const parsePts = t => { t = (t || '').trim().toLowerCase().replace(/\s/g, ''); const m = /^([\d.,]+)(k|mil|mi|m)?$/.exec(t); if (!m) return null;
   if (m[2]) { const mult = (m[2] === 'k' || m[2] === 'mil') ? 1e3 : 1e6; return Math.round(parseFloat(m[1].replace(',', '.')) * mult); }
   return parseInt(m[1].replace(/[.,]/g, ''), 10); };
 function pts() { const el = document.querySelector('[data-test-selector="copo-balance-string"]'); if (el) { const n = parsePts(el.textContent); if (n != null) post({ t: 'pts', n }); } }
 setInterval(check, 3000); setTimeout(check, 1500); setInterval(pts, 10000); setTimeout(pts, 6000);
 { const loaded = () => post({ t: 'loaded' }); if (document.readyState !== 'loading') loaded(); else document.addEventListener('DOMContentLoaded', loaded); }
})();`

type kickPage struct {
	plat    string // "kick" ou "tw"
	acct    int
	slug    string
	hwnd    uintptr
	ch      *edge.Chromium
	visible bool // janela de login/resgate
	opened  time.Time
	checker bool // página só para conferir os pontos de um canal offline (fecha depois de ler)
	loaded  bool // a página avisou que carregou (vigia de "abriu mas não carregou")
	tries   int
	url     string
	netPts  bool // já recebeu o saldo pela rede: o número da tela deixa de valer
}

var (
	kpages     = map[string]*kickPage{} // "conta|canal" → página escondida
	khosts     = map[uintptr]*kickPage{}
	kqueue     []string // páginas a abrir (uma por vez, para não travar)
	webviewOK  bool
	webviewMsg string
	kDirty     bool
	clsHost    *uint16
)

func kickInit() {
	// Sem corte de desempenho em segundo plano e sem som (vale para todas as páginas).
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS",
		"--disable-background-timer-throttling --disable-backgrounding-occluded-windows --disable-renderer-backgrounding "+
			// cada página num processo só (sem separar player, anúncios e chat em processos próprios)
			"--disable-site-isolation-trials --disable-features=CalculateNativeWinOcclusion,IsolateOrigins,site-per-process "+
			"--autoplay-policy=no-user-gesture-required --mute-audio")
	v, err := webviewloader.GetInstalledVersion()
	webviewOK = err == nil && v != ""
	if !webviewOK {
		webviewMsg = "Falta o WebView2 do Windows (motor do Edge) — instale em go.microsoft.com/fwlink/p/?LinkId=2124703"
	}
	clsHost = u16("FarmadorKickHost")
	wc := wndClassEx{WndProc: syscall.NewCallback(hostProc), Instance: hInst, ClassName: clsHost, Background: brush(cPanel)}
	wc.Cursor, _, _ = pLoadCursorW.Call(0, 32512)
	wc.Icon, _, _ = pLoadImageW.Call(hInst, 1, 1, 0, 0, 0x40)
	wc.IconSm, _, _ = pLoadImageW.Call(hInst, 1, 1, 16, 16, 0)
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
}

func kickDataDir(acct int) string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "Farmador by oSirCheese", fmt.Sprintf("kick-%02d", acct+1))
}

func kickKey(plat string, acct int, slug string) string {
	return fmt.Sprintf("%s|%d|%s", plat, acct, slug)
}

func parseKey(k string) (plat string, acct int, slug string) {
	parts := strings.SplitN(k, "|", 3)
	if len(parts) == 3 {
		plat, slug = parts[0], parts[2]
		fmt.Sscanf(parts[1], "%d", &acct)
	}
	return
}

func platName(plat string) string {
	if plat == "tw" {
		return "Twitch"
	}
	return "Kick"
}

// contaDe devolve o nome da conta (do jogo) para os títulos das janelas.
func contaDe(plat string, acct int) string {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	n := cfg.KickAccs[acct].Conta
	if plat == "tw" {
		n = cfg.TwAccs[acct].Conta
	}
	if n == "" {
		n = fmt.Sprintf("conta %02d", acct+1)
	}
	return n
}

// newKickPage cria a janela + o navegador. visible=false → escondida fora da tela.
func newKickPage(plat string, acct int, slug string, visible bool) *kickPage {
	p := &kickPage{plat: plat, acct: acct, slug: slug, visible: visible, opened: time.Now()}
	var ex, style uintptr
	x, y, w, h := -4000, -4000, 200, 112 // páginas escondidas: pequenas (menos memória de desenho)
	title := "Farmador · " + platName(plat)
	if visible {
		ex, style = 0, 0x00CF0000|0x10000000 // WS_OVERLAPPEDWINDOW|WS_VISIBLE
		w, h = S(1000), S(760)
		var wa rect
		pSystemParametersInfoW.Call(0x0030, 0, uintptr(unsafe.Pointer(&wa)), 0)
		x, y = int(wa.L)+(int(wa.R-wa.L)-w)/2, int(wa.T)+(int(wa.B-wa.T)-h)/2
		name := contaDe(plat, acct)
		if slug == "" {
			title = "Entrar na " + platName(plat) + " — " + name
		} else {
			title = platName(plat) + " · " + slug + " — " + name
		}
	} else {
		ex, style = 0x00000080|0x08000000, 0x80000000 // TOOLWINDOW|NOACTIVATE, WS_POPUP
	}
	p.hwnd, _, _ = pCreateWindowExW.Call(ex, uintptr(unsafe.Pointer(clsHost)), uintptr(unsafe.Pointer(u16(title))),
		style, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, hInst, 0)
	if p.hwnd == 0 {
		return nil
	}
	if !visible {
		pShowWindow.Call(p.hwnd, 4) // mostrado (para o vídeo rodar), mas fora da tela
	}
	khosts[p.hwnd] = p

	ch := edge.NewChromium()
	ch.SetPermission(edge.CoreWebView2PermissionKindNotifications, edge.CoreWebView2PermissionStateDeny)
	ch.MessageCallback = func(m string) { kickMessage(p, m) }
	var ok bool
	if acct > 0 && profilesOK() {
		// contas 02 em diante: perfil dentro do navegador compartilhado (pasta da conta 01)
		ok = embedProfile(ch, p.hwnd, fmt.Sprintf("kick%02d", acct+1))
	} else {
		if acct > 0 { // Edge antigo: um navegador por conta (jeito anterior)
			ch.DataPath = kickDataDir(acct)
		} else {
			ch.DataPath = kickDataDir(0)
		}
		ok = ch.Embed(p.hwnd)
	}
	if quitting { // fecharam o app enquanto a página abria: devolve o pedido de saída
		pPostQuitMessage.Call(0)
	}
	if !ok || ch.GetController() == nil {
		pDestroyWindow.Call(p.hwnd)
		delete(khosts, p.hwnd)
		return nil
	}
	p.ch = ch
	if s, err := ch.GetSettings(); err == nil && s != nil {
		// sem disfarce de navegador: a Kick recusa o login quando o navegador diz ser um e é outro
		s.PutAreDefaultContextMenusEnabled(visible)
		s.PutIsStatusBarEnabled(false)
	}
	ch.Resize()
	if !visible {
		ch.Hide() // escondida de verdade: o motor para de desenhar o vídeo (a página continua "assistindo")
	}
	switch {
	case plat == "tw" && visible:
		ch.Init(twLoginJS)
	case plat == "tw":
		ch.Init(twWatchJS)
	case visible:
		ch.Init(kickNetJS + "\n" + kickPollJS + "\n" + kickLoginJS)
	default:
		ch.Init(kickNetJS + "\n" + kickPollJS + "\n" + kickWatchJS)
	}
	u := "https://kick.com/"
	if plat == "tw" {
		u = "https://www.twitch.tv/"
	}
	if slug != "" {
		u += slug
	}
	p.url = u
	p.opened = time.Now()
	ch.Navigate(u)
	return p
}

func closeKickPage(p *kickPage) {
	if p == nil {
		return
	}
	if p.ch != nil {
		if ctl := p.ch.GetController(); ctl != nil {
			vt := *(*[32]uintptr)(unsafe.Pointer(*(*uintptr)(unsafe.Pointer(ctl))))
			syscall.SyscallN(vt[24], uintptr(unsafe.Pointer(ctl))) // ICoreWebView2Controller::Close
		}
		p.ch = nil
	}
	delete(khosts, p.hwnd)
	pDestroyWindow.Call(p.hwnd)
}

func kickMessage(p *kickPage, m string) {
	var o struct {
		T    string `json:"t"`
		N    *int   `json:"n"`
		Name string `json:"name"`
		U    string `json:"u"`
		Mine bool   `json:"mine"`
	}
	if json.Unmarshal([]byte(m), &o) != nil {
		return
	}
	if o.T == "loaded" {
		p.loaded = true
		return
	}
	if o.T == "ptsurl" { // só para o registro: quais respostas de pontos a Kick mandou
		n := "-"
		if o.N != nil {
			n = fmt.Sprint(*o.N)
		}
		mine := ""
		if !o.Mine {
			mine = " (outro canal)"
		}
		ptsLog(p, "rede vista", n, o.U+mine)
		return
	}
	if o.T == "ptsall" { // saldos pedidos direto à Kick (kickPollJS)
		kickPollResult(p, m)
		return
	}
	if o.T == "ptsnet" {
		p.netPts = true
		o.T = "pts"
	} else if o.T == "pts" && p.plat == "kick" && (p.netPts || apiFresh(p.slug)) {
		return // o número da tela pode ser velho: já há o saldo vindo do servidor
	}
	cfgMu.Lock()
	var user *string
	var pts *map[string]int
	var at *map[string]int64
	if p.plat == "tw" {
		a := &cfg.TwAccs[p.acct]
		user, pts, at = &a.User, &a.Pts, &a.PtsAt
	} else {
		a := &cfg.KickAccs[p.acct]
		user, pts, at = &a.User, &a.Pts, &a.PtsAt
	}
	switch o.T {
	case "pts":
		if o.N != nil && p.slug != "" {
			if *pts == nil {
				*pts = map[string]int{}
			}
			old := (*pts)[p.slug]
			(*pts)[p.slug] = *o.N
			if *at == nil {
				*at = map[string]int64{}
			}
			(*at)[p.slug] = time.Now().Unix()
			kDirty = true
			if old != *o.N {
				src := "tela"
				if p.netPts {
					src = "rede"
				}
				ptsLog(p, src, fmt.Sprint(*o.N), fmt.Sprintf("antes %d", old))
			}
		}
	case "user":
		if o.Name != "" || !p.visible { // nas janelas visíveis só conta quando entrou
			if *user != o.Name {
				*user = o.Name
				kDirty = true
			}
		}
	}
	cfgMu.Unlock()
	if p.checker && (o.T == "pts" && o.N != nil || o.T == "user" && o.Name == "") {
		pPostMessageW.Call(hwndMain, wmCheckDone, 0, 0) // leu (ou sem login): fecha fora deste aviso
	}
	if o.T == "user" && !p.checker {
		if hwndSet != 0 {
			pInvalidateRect.Call(hwndSet, 0, 0)
		}
		if !p.visible && o.Name == "" { // página sem login: não adianta ficar aberta
			pPostMessageW.Call(hwndMain, wmKickSync, 0, 0)
		}
	}
	onUpdate()
}

// ptsLog anota cada leitura de pontos em pontos.log (pasta do Farmador em
// %LOCALAPPDATA%), para descobrir de onde veio um número errado.
func ptsLog(p *kickPage, src, n, note string) {
	kind := "ao vivo"
	switch {
	case p.visible:
		kind = "janela"
	case p.checker:
		kind = "conferência"
	}
	ptsLogLine(platName(p.plat), kind, p.slug, src, n, note)
}

func ptsLogLine(plat, kind, slug, src, n, note string) {
	path := filepath.Join(filepath.Dir(kickDataDir(0)), "pontos.log")
	os.MkdirAll(filepath.Dir(path), 0o755)
	if st, err := os.Stat(path); err == nil && st.Size() > 512<<10 {
		os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "%s  %-6s %-12s %-16s %-11s %8s  %s\n", time.Now().Format("2006-01-02 15:04:05"),
		plat, kind, slug, src, n, note)
	f.Close()
}

// kickSync abre/fecha as páginas escondidas conforme as lives ao vivo e as contas ligadas.
func kickSync() {
	if !webviewOK {
		return
	}
	stMu.Lock()
	live := append([]string(nil), kickWatchLive...)
	stMu.Unlock()
	twMu.Lock()
	twl := append([]string(nil), twLive...)
	twMu.Unlock()
	want := map[string]bool{}
	cfgMu.Lock()
	for i, a := range cfg.KickAccs {
		if a.Watch && a.User != "" {
			for _, s := range live {
				want[kickKey("kick", i, s)] = true
			}
		}
	}
	for i, a := range cfg.TwAccs {
		if a.Watch && a.User != "" {
			for _, s := range twl {
				want[kickKey("tw", i, s)] = true
			}
		}
	}
	cfgMu.Unlock()
	for k, p := range kpages {
		if !want[k] {
			closeKickPage(p)
			delete(kpages, k)
		}
	}
	kqueue = kqueue[:0]
	for k := range want {
		if kpages[k] == nil {
			kqueue = append(kqueue, k)
		}
	}
}

// kickOpenNext abre a próxima página da fila (chamado por um timer).
var kOpening, quitting bool

// janela pedida (clique) enquanto outra página abria: abre assim que der
var pendingWin *struct {
	plat string
	acct int
	slug string
}

func kickOpenNext() {
	if pendingWin != nil && !kOpening && !quitting {
		w := pendingWin
		pendingWin = nil
		kickOpenWindow(w.plat, w.acct, w.slug)
		return
	}
	if len(kqueue) == 0 || kOpening || quitting {
		return
	}
	kOpening = true
	defer func() { kOpening = false }()
	k := kqueue[0]
	kqueue = kqueue[1:]
	if kpages[k] != nil {
		return
	}
	plat, acct, slug := parseKey(k)
	if p := newKickPage(plat, acct, slug, false); p != nil {
		p.opened = time.Now()
		kpages[k] = p
	}
}

func kickOpenWindow(plat string, acct int, slug string) {
	if quitting {
		return
	}
	if !webviewOK {
		msgBox(hwndMain, webviewMsg, appName, MB_ICONWARN)
		return
	}
	if kOpening { // outra página está abrindo agora: guarda o pedido em vez de ignorar o clique
		pendingWin = &struct {
			plat string
			acct int
			slug string
		}{plat, acct, slug}
		return
	}
	kOpening = true
	p := newKickPage(plat, acct, slug, true)
	kOpening = false
	if p != nil {
		pSetForegroundWindow.Call(p.hwnd)
	}
}

// kickWatchdog: página que abriu mas não carregou em 20 s é carregada de novo (até 3 vezes).
// Vale para as janelas que você abre e para as páginas escondidas que farmam.
func kickWatchdog() {
	for _, p := range khosts {
		if p.ch == nil || p.loaded || p.url == "" || time.Since(p.opened) < 20*time.Second {
			continue
		}
		if p.tries >= 3 {
			if p.visible && p.tries == 3 {
				p.tries++
				setText(p.hwnd, platName(p.plat)+" não carregou — aperte F5 ou feche e abra de novo")
			}
			continue
		}
		p.tries++
		p.opened = time.Now()
		p.ch.Navigate(p.url)
	}
}

func kickCloseAll() {
	for k, p := range kpages {
		closeKickPage(p)
		delete(kpages, k)
	}
	for _, p := range khosts {
		closeKickPage(p)
	}
}

func hostProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case WM_SIZE:
		if p := khosts[h]; p != nil && p.ch != nil {
			p.ch.Resize()
		}
		return 0
	case WM_CLOSE:
		if p := khosts[h]; p != nil {
			closeKickPage(p)
			if p.visible {
				pPostMessageW.Call(hwndMain, wmKickSync, 0, 0) // entrou agora: abre as lives desta conta
			}
			return 0
		}
	}
	r, _, _ := pDefWindowProcW.Call(h, uintptr(m), w, l)
	return r
}

// kickReloadOldest recarrega a página da Kick aberta há mais tempo, se passou de 30 min.
// Uma por vez (chamado a cada minuto): libera a memória que a página da live vai acumulando.
func kickReloadOldest() {
	if quitting || kOpening {
		return
	}
	var old *kickPage
	for _, p := range kpages {
		if p.ch != nil && time.Since(p.opened) >= 30*time.Minute && (old == nil || p.opened.Before(old.opened)) {
			old = p
		}
	}
	if old != nil {
		old.opened = time.Now()
		old.ch.Eval("location.reload()")
	}
}

// ---------- conferência dos pontos nos canais offline ----------
// A cada 30 min monta uma fila (conta, canal offline) e abre UMA página escondida por vez:
// lê os pontos e fecha. Assim o valor guardado nunca fica velho (sem falso 300+).

var (
	checkQueue []string
	checker    *kickPage
	checkStart time.Time
)

func checkPlan() {
	if !webviewOK {
		return
	}
	kickAPI := apiWorking() // a consulta direta à Kick já traz todos os canais (kickpoll.go)
	stMu.Lock()
	live := map[string]bool{}
	for _, c := range kickWatchLive {
		live["kick|"+c] = true
	}
	stMu.Unlock()
	twMu.Lock()
	for _, c := range twLive {
		live["tw|"+c] = true
	}
	twMu.Unlock()
	cfgMu.Lock()
	var q []string
	for i, a := range cfg.KickAccs {
		if a.User == "" || kickAPI {
			continue
		}
		for _, c := range cfg.KickWatch {
			if !live["kick|"+c] { // os ao vivo já são lidos a cada minuto pelas páginas abertas
				q = append(q, kickKey("kick", i, c))
			}
		}
	}
	for i, a := range cfg.TwAccs {
		if a.User == "" {
			continue
		}
		for _, c := range cfg.TwChannels {
			if !live["tw|"+c] {
				q = append(q, kickKey("tw", i, c))
			}
		}
	}
	cfgMu.Unlock()
	checkQueue = q
}

// checkStep roda a cada 3 s: fecha a conferência atual se acabou e abre a próxima.
func checkStep() {
	if quitting || kOpening {
		return
	}
	if checker != nil {
		if time.Since(checkStart) < 40*time.Second {
			return
		}
		checkDone() // demorou: desiste deste canal
	}
	if len(checkQueue) == 0 {
		return
	}
	k := checkQueue[0]
	checkQueue = checkQueue[1:]
	plat, acct, slug := parseKey(k)
	if kpages[k] != nil { // ficou ao vivo e já está aberto
		return
	}
	kOpening = true
	p := newKickPage(plat, acct, slug, false)
	kOpening = false
	if p != nil {
		p.checker = true
		checker, checkStart = p, time.Now()
	}
}

func checkDone() {
	if checker != nil {
		closeKickPage(checker)
		checker = nil
	}
}
