//go:build windows

package main

// Saldo de pontos da Kick quase em tempo real: pergunta direto ao servidor da Kick
// (o mesmo endereço que o site usa, /api/v2/channels/<canal>/points) a partir de uma
// página da Kick já aberta, com o login dela. Ao vivo: a cada 1 min; offline: a cada
// 3 min. Enquanto isso funcionar, a conferência que abre uma página por canal fica parada.

import (
	"encoding/json"
	"fmt"
	"time"
)

const kickPollJS = `(() => {
 if (window.__farmPollDef) return; window.__farmPollDef = 1;
 const post = o => { try { window.chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} };
 const tok = () => { const m = /(?:^|;\s*)session_token=([^;]+)/.exec(document.cookie); return m ? decodeURIComponent(m[1]) : ''; };
 const num = v => typeof v === 'number' && isFinite(v) ? Math.floor(v) : (typeof v === 'string' && /^\d+$/.test(v) ? parseInt(v, 10) : null);
 const pick = j => { if (!j || typeof j !== 'object') return null; const d = (j.data && typeof j.data === 'object' && !Array.isArray(j.data)) ? j.data : j;
   for (const k of ['points', 'balance', 'points_balance', 'channel_points']) { const n = num(d[k]); if (n != null) return n; } return null; };
 let busy = false;
 window.__farmPoll = async list => {
   if (busy) return; busy = true; const r = {}; let err = '';
   try {
     for (const s of list) {
       try {
         const h = { Accept: 'application/json' }; const t = tok(); if (t) h.Authorization = 'Bearer ' + t;
         const res = await fetch('/api/v2/channels/' + encodeURIComponent(s) + '/points', { credentials: 'include', headers: h });
         if (!res.ok) { err = 'HTTP ' + res.status; if (res.status === 401 || res.status === 403 || res.status === 429) break; continue; }
         const n = pick(await res.json()); if (n != null) r[s] = n; else err = 'resposta sem pontos';
       } catch (e) { err = String(e).slice(0, 80); }
       await new Promise(z => setTimeout(z, 250));
     }
   } finally { busy = false; post({ t: 'ptsall', r, err }); }
 };
})();`

var (
	apiAt    = map[string]time.Time{} // última vez que o servidor deu o saldo do canal
	apiOK    time.Time                // última consulta que deu certo
	apiErr   string
	pollAsk  = map[string]time.Time{} // última vez que pedimos o saldo do canal
	poller   *kickPage                // página leve da Kick, só quando nenhuma live da Kick está aberta
	pollerAt time.Time
)

// apiFresh: o saldo deste canal veio do servidor há pouco (vale mais que a tela).
func apiFresh(slug string) bool {
	return time.Since(apiAt[slug]) < 5*time.Minute
}

// apiWorking: a consulta direta está funcionando (a conferência página a página fica parada).
func apiWorking() bool {
	return time.Since(apiOK) < 10*time.Minute
}

// pollTick roda a cada 15 s na thread da janela.
func pollTick() {
	if !webviewOK || quitting {
		return
	}
	cfgMu.Lock()
	logged := cfg.KickAccs[0].User != ""
	chans := append([]string(nil), cfg.KickWatch...)
	cfgMu.Unlock()
	if !logged || len(chans) == 0 {
		return
	}
	stMu.Lock()
	live := map[string]bool{}
	for _, c := range kickWatchLive {
		live[c] = true
	}
	stMu.Unlock()

	// página que vai fazer as perguntas: uma live da Kick já aberta; senão, a página leve
	var page *kickPage
	for _, p := range kpages {
		if p.plat == "kick" && p.acct == 0 && p.ch != nil && p.loaded {
			page = p
			break
		}
	}
	if page != nil && poller != nil { // já tem live aberta: a página leve sobra
		closeKickPage(poller)
		poller = nil
	}
	if page == nil {
		if poller != nil && poller.ch == nil { // fechada (ex.: janela destruída)
			poller = nil
		}
		if poller == nil {
			if kOpening || time.Since(pollerAt) < time.Minute {
				return
			}
			kOpening = true
			// robots.txt: página da Kick quase vazia, mas no mesmo site (usa o login da conta)
			poller = newKickPage("kick", 0, "robots.txt", false)
			kOpening = false
			pollerAt = time.Now()
			if poller != nil {
				poller.slug = "" // não é canal: nada do que ela ler vira pontos de canal
			}
			return
		}
		if !poller.loaded {
			return
		}
		page = poller
	}

	now := time.Now()
	var due []string
	for _, c := range chans {
		every := 3 * time.Minute
		if live[c] {
			every = time.Minute
		}
		if now.Sub(pollAsk[c]) >= every {
			due = append(due, c)
		}
	}
	if len(due) == 0 {
		return
	}
	b, _ := json.Marshal(due)
	for _, c := range due {
		pollAsk[c] = now
	}
	page.ch.Eval("window.__farmPoll && window.__farmPoll(" + string(b) + ")")
}

// kickPollResult guarda os saldos que vieram do servidor.
func kickPollResult(p *kickPage, m string) {
	var o struct {
		R   map[string]int `json:"r"`
		Err string         `json:"err"`
	}
	if json.Unmarshal([]byte(m), &o) != nil {
		return
	}
	now := time.Now()
	cfgMu.Lock()
	a := &cfg.KickAccs[p.acct]
	if a.Pts == nil {
		a.Pts = map[string]int{}
	}
	if a.PtsAt == nil {
		a.PtsAt = map[string]int64{}
	}
	type change struct {
		slug     string
		old, new int
	}
	var ch []change
	for s, n := range o.R {
		if old, had := a.Pts[s]; !had || old != n {
			ch = append(ch, change{s, old, n})
		}
		a.Pts[s] = n
		a.PtsAt[s] = now.Unix()
		apiAt[s] = now
	}
	if len(o.R) > 0 {
		kDirty = true
		apiOK = now
	}
	cfgMu.Unlock()
	for _, c := range ch {
		ptsLogLine("Kick", "consulta", c.slug, "servidor", fmt.Sprint(c.new), fmt.Sprintf("antes %d", c.old))
	}
	if o.Err != "" && o.Err != apiErr {
		ptsLogLine("Kick", "consulta", "-", "erro", "-", o.Err)
	}
	apiErr = o.Err
	onUpdate()
}
