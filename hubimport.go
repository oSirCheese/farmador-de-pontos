package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Formato exportado pelo hub (pokeidle-hub-lives, versão 1).
type hubFile struct {
	Formato string `json:"formato"`
	Versao  int    `json:"versao"`
	Twitch  struct {
		ClientID string   `json:"clientId"`
		Canais   []string `json:"canais"`
		Contas   []struct {
			Conta  string `json:"conta"`
			Slot   string `json:"slot"`
			Login  string `json:"login"`
			Logada bool   `json:"logada"`
			Acesso *struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
			} `json:"acesso"`
		} `json:"contas"`
	} `json:"twitch"`
	Kick struct {
		Canais []string `json:"canais"`
		Contas []struct {
			Conta  string         `json:"conta"`
			Logada bool           `json:"logada"`
			Pontos map[string]int `json:"pontos"`
		} `json:"contas"`
	} `json:"kick"`
}

func cleanList(in []string, kick bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range in {
		s := parseSlug(c)
		if !kick {
			s = strings.TrimPrefix(strings.TrimPrefix(s, "twitch.tv/"), "#")
		}
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// importHub lê o arquivo do hub. Não mexe na lista de canais para DIGITAR.
func importHub(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var h hubFile
	if err := json.Unmarshal(b, &h); err != nil {
		return "", errors.New("o arquivo não é um JSON válido")
	}
	if h.Formato != "pokeidle-hub-lives" {
		return "", errors.New("este arquivo não foi exportado pelo hub (formato diferente)")
	}
	if h.Versao != 1 {
		return "", fmt.Errorf("versão %d do arquivo não é suportada", h.Versao)
	}
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if h.Twitch.ClientID != "" {
		cfg.TwClientID = h.Twitch.ClientID
	}
	cfg.TwChannels = cleanList(h.Twitch.Canais, false)
	tok := 0
	for i := 0; i < len(h.Twitch.Contas); i++ {
		c := h.Twitch.Contas[i]
		slot := i
		if len(c.Slot) == 2 && c.Slot[0] == 't' && c.Slot[1] >= '1' && c.Slot[1] <= '4' {
			slot = int(c.Slot[1] - '1')
		}
		if slot >= nAccs { // só a conta 01 é usada
			continue
		}
		a := &cfg.TwAccs[slot]
		a.Conta = c.Conta
		if c.Login != "" {
			a.Login = c.Login
		}
		if c.Acesso != nil && c.Acesso.AccessToken != "" {
			a.Access, a.Refresh = c.Acesso.AccessToken, c.Acesso.RefreshToken
			tok++
		}
	}
	cfg.KickWatch = cleanList(h.Kick.Canais, true)
	for i := 0; i < nAccs && i < len(h.Kick.Contas); i++ {
		c := h.Kick.Contas[i]
		a := &cfg.KickAccs[i]
		a.Conta = c.Conta
		if a.Pts == nil {
			a.Pts = map[string]int{}
		}
		for s, n := range c.Pontos {
			if _, ok := a.Pts[s]; !ok { // não sobrescreve valores mais novos já lidos aqui
				a.Pts[s] = n
			}
		}
	}
	cfg.KickAccs[0].Watch = true
	cfg.TwAccs[0].Watch = true
	cfg.LastImport = fmt.Sprintf("%s · %s · %d contas", filepath.Base(path), time.Now().Format("02/01 15:04"), min(nAccs, max(len(h.Twitch.Contas), len(h.Kick.Contas))))
	msg := fmt.Sprintf("Importado: %d canais da Twitch, %d da Kick, %d acessos da Twitch.", len(cfg.TwChannels), len(cfg.KickWatch), tok)
	if tok == 0 {
		msg += "\nO arquivo veio sem os acessos da Twitch: entre na conta em Contas."
	}
	msg += "\nNa Kick, a conta precisa entrar uma vez (Contas → Entrar na Kick)."
	return msg, nil
}

// migrateV2 prepara a configuração antiga para a v2.0.
func migrateV2() {
	// v3.1: na Twitch os nomes das contas do jogo vieram invertidos do hub; usa os da Kick (mesma conta, mesmo número)
	for i := range cfg.TwAccs {
		if cfg.KickAccs[i].Conta != "" {
			cfg.TwAccs[i].Conta = cfg.KickAccs[i].Conta
		}
	}
	if cfg.ProfVer < 1 { // v2.13: contas 02–04 mudaram de navegador; precisam entrar na Kick uma vez
		for i := 1; i < nAccs; i++ {
			cfg.KickAccs[i].User = ""
		}
		cfg.ProfVer = 1
	}
	if len(cfg.Phrases) == 0 {
		for _, m := range cfg.Messages {
			if t := strings.TrimSpace(m); t != "" {
				cfg.Phrases = append(cfg.Phrases, Phrase{t, true})
			}
		}
	}
	for i := range cfg.KickAccs {
		if cfg.KickAccs[i].Pts == nil {
			cfg.KickAccs[i].Pts = map[string]int{}
		}
	}
	if cfg.ProfVer < 2 { // v3.4: a conta 01 deixou de ser "sempre ligada"; começa ligada, como estava
		cfg.KickAccs[0].Watch = true
		cfg.TwAccs[0].Watch = true
		cfg.ProfVer = 2
	}
	if cfg.KickSel < 0 || cfg.KickSel >= nAccs {
		cfg.KickSel = 0
	}
	if cfg.TwSel < 0 || cfg.TwSel >= nAccs {
		cfg.TwSel = 0
	}
}

// activePhrases devolve as frases marcadas.
func activePhrases() []string {
	var out []string
	for _, p := range cfg.Phrases {
		if p.On && strings.TrimSpace(p.Text) != "" {
			out = append(out, p.Text)
		}
	}
	return out
}
