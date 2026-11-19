package main

// Twitch pelo caminho oficial, igual ao hub: chat por IRC (WebSocket), sem vídeo.
// As contas só ficam presentes no chat; nunca enviam mensagens.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	twDevice   = "https://id.twitch.tv/oauth2/device"
	twToken    = "https://id.twitch.tv/oauth2/token"
	twValidate = "https://id.twitch.tv/oauth2/validate"
	twIRC      = "wss://irc-ws.chat.twitch.tv:443"
	twScopes   = "chat:read"
)

type twStatus struct {
	State string // "parado", "conectando", "no chat", "reconectando", "sem login", "login expirou"
	Login string
}

var (
	twMu      sync.Mutex
	twStat    [4]twStatus
	twLive    []string
	twStop    [4]chan struct{}
	twGen     int
	twLogging [4]bool
)

func twSet(i int, st string) {
	twMu.Lock()
	twStat[i].State = st
	twMu.Unlock()
	onUpdate()
}

func twPost(u string, form url.Values) (map[string]any, int, error) {
	resp, err := httpc.PostForm(u, form)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var j map[string]any
	json.NewDecoder(resp.Body).Decode(&j)
	return j, resp.StatusCode, nil
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// twValidateTok: login e segundos restantes; ok=false se o token não vale; err se sem rede.
func twValidateTok(tok string) (login string, left int, ok bool, err error) {
	req, _ := http.NewRequest("GET", twValidate, nil)
	req.Header.Set("Authorization", "OAuth "+tok)
	resp, err := httpc.Do(req)
	if err != nil {
		return "", 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", 0, false, nil
	}
	var j struct {
		Login     string `json:"login"`
		ExpiresIn int    `json:"expires_in"`
	}
	json.NewDecoder(resp.Body).Decode(&j)
	return j.Login, j.ExpiresIn, j.Login != "", nil
}

func twRefresh(i int) bool {
	cfgMu.Lock()
	rt, cid := cfg.TwAccs[i].Refresh, cfg.TwClientID
	cfgMu.Unlock()
	if rt == "" || cid == "" {
		return false
	}
	j, code, err := twPost(twToken, url.Values{"client_id": {cid}, "grant_type": {"refresh_token"}, "refresh_token": {rt}})
	if err != nil || code != 200 || str(j, "access_token") == "" {
		return false
	}
	cfgMu.Lock()
	cfg.TwAccs[i].Access = str(j, "access_token")
	if r := str(j, "refresh_token"); r != "" {
		cfg.TwAccs[i].Refresh = r
	}
	cfgMu.Unlock()
	saveConfig()
	return true
}

// twGoodToken devolve um token válido (renova se precisar). "" = precisa entrar de novo.
func twGoodToken(i int) (string, string) {
	cfgMu.Lock()
	tok := cfg.TwAccs[i].Access
	cfgMu.Unlock()
	if tok == "" {
		return "", ""
	}
	login, left, ok, err := twValidateTok(tok)
	if err != nil {
		return tok, "" // sem rede agora: tenta com o que tem
	}
	if ok && left > 600 {
		return tok, login
	}
	if twRefresh(i) {
		cfgMu.Lock()
		tok = cfg.TwAccs[i].Access
		cfgMu.Unlock()
		login, _, _, _ = twValidateTok(tok)
		return tok, login
	}
	if ok {
		return tok, login
	}
	return "", ""
}

// twApply (re)inicia as 4 conexões com a lista atual de canais.
func twApply() {
	twMu.Lock()
	for i := range twStop {
		if twStop[i] != nil {
			close(twStop[i])
			twStop[i] = nil
		}
	}
	twGen++
	cfgMu.Lock()
	chans := append([]string(nil), cfg.TwChannels...)
	var has [4]bool
	for i := range cfg.TwAccs {
		has[i] = cfg.TwAccs[i].Access != ""
	}
	cfgMu.Unlock()
	for i := 0; i < 4; i++ {
		switch {
		case !has[i]:
			twStat[i].State = "sem login"
		case len(chans) == 0:
			twStat[i].State = "parado"
		default:
			stop := make(chan struct{})
			twStop[i] = stop
			twStat[i].State = "conectando"
			go twChatter(i, chans, stop)
		}
	}
	twMu.Unlock()
	onUpdate()
}

func sleepOrStop(d time.Duration, stop chan struct{}) bool {
	select {
	case <-time.After(d):
		return true
	case <-stop:
		return false
	}
}

func twChatter(i int, chans []string, stop chan struct{}) {
	fails := 0
	for {
		select {
		case <-stop:
			return
		default:
		}
		tok, login := twGoodToken(i)
		if tok == "" {
			twSet(i, "login expirou")
			return
		}
		if login == "" {
			cfgMu.Lock()
			login = cfg.TwAccs[i].Login
			cfgMu.Unlock()
		} else {
			twMu.Lock()
			twStat[i].Login = login
			twMu.Unlock()
			cfgMu.Lock()
			cfg.TwAccs[i].Login = login
			cfgMu.Unlock()
		}
		res := twSession(i, tok, login, chans, stop)
		switch res {
		case "stop":
			return
		case "auth":
			if !twRefresh(i) {
				twSet(i, "login expirou")
				return
			}
			fails = 0
			continue
		}
		fails++
		twSet(i, "reconectando")
		wait := time.Duration(4<<min(fails, 5)) * time.Second
		if !sleepOrStop(min(wait, 2*time.Minute), stop) {
			return
		}
	}
}

// twSession: uma conexão; devolve "stop", "auth" (login falhou) ou "drop".
func twSession(i int, tok, login string, chans []string, stop chan struct{}) string {
	c, _, err := websocket.DefaultDialer.Dial(twIRC, nil)
	if err != nil {
		return "drop"
	}
	defer c.Close()
	var wmu sync.Mutex
	send := func(s string) {
		wmu.Lock()
		c.WriteMessage(websocket.TextMessage, []byte(s))
		wmu.Unlock()
	}
	send("CAP REQ :twitch.tv/membership")
	send("PASS oauth:" + tok)
	send("NICK " + strings.ToLower(login))

	done := make(chan struct{})
	defer close(done)
	go func() { // fecha a conexão quando mandarem parar
		select {
		case <-stop:
			c.Close()
		case <-done:
		}
	}()
	for {
		c.SetReadDeadline(time.Now().Add(8 * time.Minute)) // a Twitch manda PING a cada ~5 min
		_, data, err := c.ReadMessage()
		if err != nil {
			select {
			case <-stop:
				return "stop"
			default:
				return "drop"
			}
		}
		for _, line := range strings.Split(string(data), "\r\n") {
			switch {
			case line == "":
			case strings.HasPrefix(line, "PING"):
				send("PONG" + strings.TrimPrefix(line, "PING"))
			case strings.Contains(line, " 001 "):
				twSet(i, "no chat")
				go func() { // limite da Twitch: 20 entradas a cada 10 s
					for _, ch := range chans {
						if !sleepOrStop(700*time.Millisecond, stop) {
							return
						}
						send("JOIN #" + ch)
					}
				}()
			case strings.Contains(line, "Login authentication failed"), strings.Contains(line, "Login unsuccessful"), strings.Contains(line, "Improperly formatted auth"):
				return "auth"
			case strings.Contains(line, " RECONNECT"):
				return "drop"
			}
		}
	}
}

// twPollLive conta quantos canais da lista estão ao vivo (API Helix).
// twLiveErr: "" ok · "auth" nenhum acesso da Twitch funciona (precisa autorizar) · "rede" sem conexão agora
var twLiveErr string

// twQueryLive consulta quais canais estão ao vivo com um token. code = status HTTP (0 = sem rede).
func twQueryLive(cid, tok string, chans []string) ([]string, int) {
	var live []string
	for s := 0; s < len(chans); s += 100 {
		q := url.Values{"first": {"100"}}
		for _, c := range chans[s:min(s+100, len(chans))] {
			q.Add("user_login", c)
		}
		req, _ := http.NewRequest("GET", "https://api.twitch.tv/helix/streams?"+q.Encode(), nil)
		req.Header.Set("Client-Id", cid)
		req.Header.Set("Authorization", "Bearer "+tok)
		r, err := httpc.Do(req)
		if err != nil {
			return nil, 0
		}
		if r.StatusCode != 200 {
			r.Body.Close()
			return nil, r.StatusCode
		}
		var j struct {
			Data []struct {
				UserLogin string `json:"user_login"`
			} `json:"data"`
		}
		json.NewDecoder(r.Body).Decode(&j)
		r.Body.Close()
		for _, d := range j.Data {
			live = append(live, strings.ToLower(d.UserLogin))
		}
	}
	return live, 200
}

// twPollLive conta quais canais da lista estão ao vivo (API oficial da Twitch).
// Tenta o acesso de cada conta até um funcionar; acesso morto (recusado e sem renovação) é
// apagado para o botão "Autorizar" voltar a aparecer, e o balão avisa.
func twPollLive() {
	cfgMu.Lock()
	var chans []string
	for _, c := range cfg.TwChannels { // a Twitch recusa a consulta inteira se um nome vier torto
		c = strings.ToLower(strings.TrimSpace(c))
		if i := strings.LastIndex(c, "/"); i >= 0 {
			c = c[i+1:]
		}
		c = strings.TrimPrefix(c, "@")
		if twNameOK(c) {
			chans = append(chans, c)
		}
	}
	cid := cfg.TwClientID
	var accs []int
	for i, a := range cfg.TwAccs {
		if a.Access != "" || a.Refresh != "" {
			accs = append(accs, i)
		}
	}
	cfgMu.Unlock()
	setErr := func(e string) {
		twMu.Lock()
		twLiveErr = e
		twMu.Unlock()
	}
	if len(chans) == 0 {
		twMu.Lock()
		twLive = nil
		twMu.Unlock()
		setErr("")
		return
	}
	if cid == "" || len(accs) == 0 {
		setErr("auth")
		return
	}
	offline := false
	for _, i := range accs {
		tok, _ := twGoodToken(i) // confere e renova se precisar
		if tok == "" {
			tok = twTokenOf(i)
		}
		live, code := twQueryLive(cid, tok, chans)
		if code == 401 && twRefresh(i) {
			live, code = twQueryLive(cid, twTokenOf(i), chans)
		}
		switch code {
		case 200:
			twMu.Lock()
			twLive = live
			twLiveErr = ""
			twMu.Unlock()
			return
		case 401:
			// recusado e sem renovação: acesso morto
			cfgMu.Lock()
			cfg.TwAccs[i].Access, cfg.TwAccs[i].Refresh = "", ""
			cfgMu.Unlock()
			saveConfig()
		default: // sem rede, limite da Twitch, erro do servidor: tenta a próxima conta
			offline = true
		}
	}
	if offline {
		setErr("rede") // mantém a última contagem
	} else {
		setErr("auth")
	}
}

func twNameOK(c string) bool {
	if c == "" || len(c) > 25 {
		return false
	}
	for _, r := range c {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func twTokenOf(i int) string {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return cfg.TwAccs[i].Access
}

// twDeviceLogin: login por código para uma conta (abre a página de ativação da Twitch).
func twDeviceLogin(i int) error {
	cfgMu.Lock()
	cid := cfg.TwClientID
	cfgMu.Unlock()
	if cid == "" {
		return errors.New("falta o Client ID da Twitch: importe o arquivo do hub primeiro")
	}
	j, code, err := twPost(twDevice, url.Values{"client_id": {cid}, "scopes": {twScopes}})
	if err != nil {
		return err
	}
	if code != 200 || str(j, "device_code") == "" {
		return errors.New("a Twitch recusou o Client ID: " + str(j, "message"))
	}
	openURL(str(j, "verification_uri"))
	interval, _ := j["interval"].(float64)
	expires, _ := j["expires_in"].(float64)
	wait := time.Duration(max(2, int(interval))) * time.Second
	end := time.Now().Add(time.Duration(max(60, int(expires))) * time.Second)
	for time.Now().Before(end) {
		time.Sleep(wait)
		r, code, err := twPost(twToken, url.Values{"client_id": {cid}, "scopes": {twScopes}, "device_code": {str(j, "device_code")},
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}})
		if err != nil {
			continue
		}
		if code == 200 && str(r, "access_token") != "" {
			cfgMu.Lock()
			cfg.TwAccs[i].Access, cfg.TwAccs[i].Refresh = str(r, "access_token"), str(r, "refresh_token")
			cfgMu.Unlock()
			if login, _, ok, _ := twValidateTok(str(r, "access_token")); ok {
				cfgMu.Lock()
				cfg.TwAccs[i].Login = login
				cfgMu.Unlock()
			}
			saveConfig()
			return nil
		}
		m := strings.ToLower(str(r, "message"))
		switch {
		case strings.Contains(m, "pending"):
		case strings.Contains(m, "slow_down"):
			wait += 5 * time.Second
		default:
			return errors.New("a Twitch não autorizou: " + str(r, "message"))
		}
	}
	return errors.New("o código expirou; clique em Entrar de novo")
}
