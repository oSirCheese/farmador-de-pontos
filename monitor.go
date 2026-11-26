package main

import (
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Estado de cada canal, lido pelo balão para desenhar.
type chanState struct {
	Slug     string
	Live     bool
	Since    time.Time
	LastSent time.Time
	LastMsg  string
	Err      string
	seqIdx   int
	lastIdx  int
}

var (
	stMu          sync.Mutex
	states        = map[string]*chanState{}
	globalErr     string    // problema geral (login, internet)
	kickWatchLive []string  // canais da lista de assistir que estão ao vivo
	lastCheck     time.Time // última verificação concluída

	sending  atomic.Bool // "Rodando": envia frases
	wakeCh   = make(chan struct{}, 1)
	onUpdate = func() {} // definido pela interface: pede redesenho
)

// wake faz o monitor verificar agora (após salvar configurações, login, Iniciar).
func wake() {
	select {
	case wakeCh <- struct{}{}:
	default:
	}
}

type snapshot struct {
	Channels  []string
	States    map[string]chanState
	GlobalErr string
	LastCheck time.Time
	Sending   bool
	Interval  time.Duration
	Order     string
	LoggedIn  bool
	Username  string
	PhrOn     int
	PhrAll    int
	// lives
	TwStat    [nAccs]twStatus
	TwLive    int
	TwChans   int
	TwNames   [nAccs]string
	KickWatch []string
	KickLive  map[string]bool
	KickAccs  [nAccs]KickAcc
	KickSel   int
	TwAccsW   [nAccs]TwAcc
	TwSel     int
	TwWatch   []string
	TwLiveSet map[string]bool
	TwLiveErr string
}

func takeSnapshot() snapshot {
	cfgMu.Lock()
	s := snapshot{
		Channels:  append([]string(nil), cfg.Channels...),
		Interval:  time.Duration(cfg.IntervalMin) * time.Minute,
		Order:     cfg.Order,
		LoggedIn:  cfg.RefreshToken != "",
		Username:  cfg.Username,
		PhrAll:    len(cfg.Phrases),
		TwChans:   len(cfg.TwChannels),
		KickWatch: append([]string(nil), cfg.KickWatch...),
		KickAccs:  cfg.KickAccs,
		KickSel:   cfg.KickSel,
		TwAccsW:   cfg.TwAccs,
		TwSel:     cfg.TwSel,
		TwWatch:   append([]string(nil), cfg.TwChannels...),
	}
	for _, p := range cfg.Phrases {
		if p.On {
			s.PhrOn++
		}
	}
	for i, a := range cfg.TwAccs {
		s.TwNames[i] = a.Login
		if s.TwNames[i] == "" {
			s.TwNames[i] = a.Conta
		}
	}
	cfgMu.Unlock()
	stMu.Lock()
	s.States = make(map[string]chanState, len(states))
	for k, v := range states {
		s.States[k] = *v
	}
	s.GlobalErr = globalErr
	s.LastCheck = lastCheck
	s.KickLive = map[string]bool{}
	for _, c := range kickWatchLive {
		s.KickLive[c] = true
	}
	stMu.Unlock()
	twMu.Lock()
	s.TwStat = twStat
	s.TwLive = len(twLive)
	s.TwLiveErr = twLiveErr
	s.TwLiveSet = map[string]bool{}
	for _, c := range twLive {
		s.TwLiveSet[c] = true
	}
	twMu.Unlock()
	s.Sending = sending.Load()
	return s
}

// monitor roda o tempo todo: verifica as lives a cada 1 min (ou quando acordado)
// e, se "Rodando", manda uma frase nos canais ao vivo respeitando o intervalo.
func monitor() {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	n := 0
	go func() { // zip de versão nova em Downloads / Área de Trabalho: confere a cada 5 s (só lê duas pastas)
		for {
			scanUpdate()
			time.Sleep(5 * time.Second)
		}
	}()
	for {
		offcCheck()
		checkOnce()
		if n%2 == 0 { // Twitch: a cada ~2 min
			twPollLive()
		}
		n++
		pPostMessageW.Call(hwndMain, wmKickSync, 0, 0)
		onUpdate()
		trimMemory()
		select {
		case <-tick.C:
		case <-wakeCh:
		}
	}
}

func checkOnce() {
	cfgMu.Lock()
	chans := append([]string(nil), cfg.Channels...)
	msgs := activePhrases()
	watch := append([]string(nil), cfg.KickWatch...)
	order := cfg.Order
	interval := time.Duration(cfg.IntervalMin) * time.Minute
	logged := cfg.RefreshToken != ""
	cfgMu.Unlock()

	// remove canais que saíram da lista
	stMu.Lock()
	keep := map[string]bool{}
	for _, c := range chans {
		keep[c] = true
		if states[c] == nil {
			states[c] = &chanState{Slug: c, lastIdx: -1}
		}
	}
	for k := range states {
		if !keep[k] {
			delete(states, k)
		}
	}
	stMu.Unlock()

	if !logged {
		setGlobalErr("Autorize a oSirCheese na engrenagem → Contas")
		return
	}
	// uma consulta só para os canais de digitar e de assistir
	all := append([]string(nil), chans...)
	seenC := map[string]bool{}
	for _, c := range chans {
		seenC[c] = true
	}
	for _, c := range watch {
		if !seenC[c] {
			seenC[c] = true
			all = append(all, c)
		}
	}
	if len(all) == 0 {
		setGlobalErr("")
		return
	}
	infos, errs := fetchChannels(all)
	var wl []string
	for _, c := range watch {
		if infos[c].Live {
			wl = append(wl, c)
		}
	}
	stMu.Lock()
	kickWatchLive = wl
	stMu.Unlock()
	if len(errs) == len(all) {
		for _, e := range errs { // todos falharam: problema geral
			setGlobalErr(short(friendlyErr(e.Error())))
			break
		}
	} else {
		setGlobalErr("")
	}

	now := time.Now()
	type job struct {
		slug string
		id   int64
	}
	var due []job
	stMu.Lock()
	for _, c := range chans {
		st := states[c]
		if e, bad := errs[c]; bad {
			st.Err = friendlyErr(e.Error())
			continue
		}
		st.Err = ""
		info := infos[c]
		if info.Live && !st.Live {
			st.Since = info.Since
			if st.Since.IsZero() {
				st.Since = now
			}
		}
		if info.Live && !info.Since.IsZero() {
			st.Since = info.Since
		}
		st.Live = info.Live
		if st.Live && sending.Load() && len(msgs) > 0 && now.Sub(st.LastSent) >= interval {
			due = append(due, job{c, info.ID})
		}
	}
	lastCheck = now
	stMu.Unlock()

	for _, j := range due {
		stMu.Lock()
		st := states[j.slug]
		var i int
		if order == "sequencia" {
			i = st.seqIdx % len(msgs)
		} else {
			i = rand.Intn(len(msgs))
			if len(msgs) > 1 && i == st.lastIdx {
				i = (i + 1 + rand.Intn(len(msgs)-1)) % len(msgs)
			}
		}
		stMu.Unlock()
		err := sendChat(j.id, msgs[i])
		stMu.Lock()
		if st = states[j.slug]; st != nil {
			if err != nil {
				st.Err = "falha ao enviar: " + friendlyErr(err.Error())
			} else {
				st.LastSent = time.Now()
				st.LastMsg = msgs[i]
				st.lastIdx = i
				st.seqIdx = i + 1
			}
		}
		stMu.Unlock()
	}
}

func setGlobalErr(s string) {
	stMu.Lock()
	globalErr = s
	stMu.Unlock()
}

func friendlyErr(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "sem login"):
		return "Faça o login da Kick na engrenagem (configurações)"
	case strings.Contains(l, "token http 4"), strings.Contains(l, "não autorizado"), strings.Contains(l, "http 401"):
		return "Login expirou — entre de novo na engrenagem (configurações)"
	case strings.Contains(l, "no such host"), strings.Contains(l, "dial tcp"), strings.Contains(l, "timeout"),
		strings.Contains(l, "eof"), strings.Contains(l, "connection reset"), strings.Contains(l, "forcibly closed"),
		strings.Contains(l, "wsarecv"), strings.Contains(l, "wsasend"), strings.Contains(l, "broken pipe"),
		strings.Contains(l, "connection refused"), strings.Contains(l, "network is unreachable"):
		return "Sem conexão com a Kick — tentando de novo"
	case strings.Contains(l, "não encontrado"):
		return "canal não encontrado"
	}
	return s
}

func short(s string) string {
	r := []rune(s)
	if len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}
