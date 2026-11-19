package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	authURL     = "https://id.kick.com/oauth/authorize"
	tokenURL    = "https://id.kick.com/oauth/token"
	apiBase     = "https://api.kick.com/public/v1"
	redirectURI = "http://localhost:53682/callback"
	scopes      = "user:read channel:read chat:write"
)

// Uma conexão reaproveitada entre as checagens (evita refazer o TLS a cada minuto).
var httpc = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     150 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   false,
	},
}

type Config struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Message      string   `json:"message"` // v1.4 e anteriores (vira a 1ª frase da lista)
	Messages     []string `json:"messages"`
	Order        string   `json:"order"` // "sorteada" ou "sequencia"
	IntervalMin  int      `json:"interval_min"`
	Channels     []string `json:"channels"`
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresAt    int64    `json:"expires_at"`
	Username     string   `json:"username"`
	// v2.0+: lives (Twitch no chat, Kick assistindo) e frases marcáveis
	Phrases    []Phrase   `json:"phrases"`
	TwClientID string     `json:"tw_client_id"`
	TwChannels []string   `json:"tw_channels"`
	TwAccs     [4]TwAcc   `json:"tw_accs"`
	KickWatch  []string   `json:"kick_watch"`
	KickAccs   [4]KickAcc `json:"kick_accs"`
	KickSel    int        `json:"kick_sel"`
	TwSel      int        `json:"tw_sel"`
	LastImport string     `json:"last_import"`
	// v1.6+
	CfgVer    int  `json:"cfg_ver"`
	ProfVer   int  `json:"prof_ver"`  // 1 = contas 02–04 já em perfis do navegador compartilhado
	Sending   bool `json:"sending"`   // estava "Rodando" ao fechar → volta rodando
	Collapsed bool `json:"collapsed"` // balão minimizado
	X         int  `json:"x"`
	Y         int  `json:"y"`
	HasPos    bool `json:"has_pos"`
}

type Phrase struct {
	Text string `json:"text"`
	On   bool   `json:"on"`
}

type TwAcc struct {
	Conta   string `json:"conta"`
	Login   string `json:"login"`
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	// v3.0: assistir na Twitch igual à Kick (página da live aberta)
	Watch bool             `json:"watch"`  // a 01 assiste sempre
	User  string           `json:"user"`   // logada no site da Twitch (nome detectado na página)
	Pts   map[string]int   `json:"pts"`    // último valor de pontos por canal
	PtsAt map[string]int64 `json:"pts_at"` // quando cada valor foi conferido (unix)
}

type KickAcc struct {
	Conta string           `json:"conta"`
	Watch bool             `json:"watch"`  // a 01 assiste sempre
	User  string           `json:"user"`   // nome na Kick detectado na página
	Pts   map[string]int   `json:"pts"`    // último valor de pontos por canal
	PtsAt map[string]int64 `json:"pts_at"` // quando cada valor foi conferido (unix)
}

var (
	cfgMu sync.Mutex
	cfg   Config
)

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func randStr(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenRequest(form url.Values) error {
	resp, err := httpc.PostForm(tokenURL, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("token HTTP %d: %s", resp.StatusCode, trim(body))
	}
	var t tokenResp
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		return fmt.Errorf("resposta de token inválida: %s", trim(body))
	}
	cfgMu.Lock()
	cfg.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		cfg.RefreshToken = t.RefreshToken
	}
	cfg.ExpiresAt = time.Now().Unix() + t.ExpiresIn
	cfgMu.Unlock()
	saveConfig()
	return nil
}

// Login OAuth 2.1 com PKCE: abre o navegador padrão e espera o retorno em localhost.
func doLogin() error {
	cfgMu.Lock()
	id, secret := cfg.ClientID, cfg.ClientSecret
	cfgMu.Unlock()
	if id == "" || secret == "" {
		return errors.New("preencha Client ID e Client Secret")
	}
	verifier := randStr(48)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := randStr(16)

	ln, err := net.Listen("tcp", "127.0.0.1:53682")
	if err != nil {
		return fmt.Errorf("porta 53682 ocupada: %v", err)
	}
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if q.Get("state") != state {
			io.WriteString(w, "Erro: state inválido.")
			errCh <- errors.New("state inválido")
			return
		}
		if e := q.Get("error"); e != "" {
			io.WriteString(w, "Login cancelado: "+e)
			errCh <- errors.New("login cancelado: " + e)
			return
		}
		io.WriteString(w, "<h3 style='font-family:sans-serif'>Login feito! Pode fechar esta aba e voltar ao Kick Timer.</h3>")
		codeCh <- q.Get("code")
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	u := authURL + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {id},
		"redirect_uri":          {redirectURI},
		"scope":                 {scopes},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}.Encode()
	openURL(u)

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Minute):
		return errors.New("tempo esgotado esperando o login")
	}
	if err := tokenRequest(url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {id},
		"client_secret": {secret},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}); err != nil {
		return err
	}
	if name, err := fetchUsername(); err == nil {
		cfgMu.Lock()
		cfg.Username = name
		cfgMu.Unlock()
		saveConfig()
	}
	return nil
}

func refresh() error {
	cfgMu.Lock()
	rt, id, secret := cfg.RefreshToken, cfg.ClientID, cfg.ClientSecret
	cfgMu.Unlock()
	if rt == "" {
		return errors.New("sem login; clique em Login Kick")
	}
	err := tokenRequest(url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rt},
		"client_id":     {id},
		"client_secret": {secret},
	})
	// a Kick recusou a renovação: a autorização acabou. Limpa para o botão Autorizar voltar a aparecer.
	if err != nil && strings.Contains(err.Error(), "token HTTP 4") {
		cfgMu.Lock()
		cfg.RefreshToken, cfg.AccessToken = "", ""
		cfgMu.Unlock()
		saveConfig()
		return errors.New("a autorização da Kick expirou: autorize de novo na engrenagem → Contas")
	}
	return err
}

func ensureToken() error {
	cfgMu.Lock()
	tok, exp := cfg.AccessToken, cfg.ExpiresAt
	cfgMu.Unlock()
	if tok == "" {
		return errors.New("sem login; clique em Login Kick")
	}
	if time.Now().Unix() > exp-300 {
		return refresh()
	}
	return nil
}

// api faz a chamada e tenta renovar o token uma vez se receber 401.
func api(method, path string, payload any) ([]byte, int, error) {
	if err := ensureToken(); err != nil {
		return nil, 0, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		var rd io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, apiBase+path, rd)
		cfgMu.Lock()
		req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)
		cfgMu.Unlock()
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := httpc.Do(req)
		if err != nil {
			return nil, 0, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 401 && attempt == 0 {
			if err := refresh(); err != nil {
				return nil, 401, err
			}
			continue
		}
		return body, resp.StatusCode, nil
	}
	return nil, 401, errors.New("não autorizado")
}

func fetchUsername() (string, error) {
	body, code, err := api("GET", "/users", nil)
	if err != nil {
		return "", err
	}
	if code != 200 {
		return "", fmt.Errorf("HTTP %d", code)
	}
	var r struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	json.Unmarshal(body, &r)
	if len(r.Data) == 0 {
		return "", errors.New("sem usuário")
	}
	return r.Data[0].Name, nil
}

type chanInfo struct {
	ID    int64
	Live  bool
	Since time.Time // início da live (zero se a Kick não informar)
}

type channelsResp struct {
	Data []struct {
		BroadcasterUserID int64  `json:"broadcaster_user_id"`
		Slug              string `json:"slug"`
		Stream            *struct {
			IsLive    bool   `json:"is_live"`
			StartTime string `json:"start_time"`
		} `json:"stream"`
	} `json:"data"`
}

func queryChannels(slugs []string) (map[string]chanInfo, int, []byte, error) {
	q := url.Values{}
	for _, s := range slugs {
		q.Add("slug", s)
	}
	body, code, err := api("GET", "/channels?"+q.Encode(), nil)
	if err != nil || code != 200 {
		return nil, code, body, err
	}
	var r channelsResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, code, body, err
	}
	out := map[string]chanInfo{}
	for _, d := range r.Data {
		ci := chanInfo{ID: d.BroadcasterUserID, Live: d.Stream != nil && d.Stream.IsLive}
		if ci.Live && d.Stream.StartTime != "" {
			for _, f := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05"} {
				if t, err := time.Parse(f, d.Stream.StartTime); err == nil && t.Year() > 2000 {
					ci.Since = t
					break
				}
			}
		}
		out[strings.ToLower(d.Slug)] = ci
	}
	return out, code, body, nil
}

// fetchChannels consulta todos os canais numa só requisição (até 50 por vez).
// Se algum nome não existir a Kick recusa o lote inteiro; aí consulta um a um
// para apontar qual está errado. Retorna também os erros por canal.
func fetchChannels(slugs []string) (map[string]chanInfo, map[string]error) {
	res := map[string]chanInfo{}
	errs := map[string]error{}
	for i := 0; i < len(slugs); i += 50 {
		batch := slugs[i:min(i+50, len(slugs))]
		m, code, body, err := queryChannels(batch)
		if err == nil && code == 200 {
			for _, s := range batch {
				if c, ok := m[s]; ok {
					res[s] = c
				} else {
					errs[s] = errors.New("canal não encontrado")
				}
			}
			continue
		}
		if err == nil && code != 400 {
			err = fmt.Errorf("HTTP %d: %s", code, trim(body))
		}
		if err != nil {
			for _, s := range batch {
				errs[s] = err
			}
			continue
		}
		for _, s := range batch { // 400: algum nome inválido no lote
			m, code, _, err := queryChannels([]string{s})
			switch {
			case err != nil:
				errs[s] = err
			case code != 200 || len(m) == 0:
				errs[s] = errors.New("canal não encontrado — confira o nome")
			default:
				for _, c := range m {
					res[s] = c
				}
			}
		}
	}
	return res, errs
}

func sendChat(broadcasterID int64, text string) error {
	body, code, err := api("POST", "/chat", map[string]any{
		"type":                "user",
		"broadcaster_user_id": broadcasterID,
		"content":             text,
	})
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("HTTP %d: %s", code, trim(body))
	}
	return nil
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
