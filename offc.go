//go:build windows

package main

// Lista oficial de parceiros do jogo (Bônus Twitch / Bônus na Kick).
// O hub lê pela conta 01 e grava em %APPDATA%\Farmador by oSirCheese\lista-oficial.json;
// o Farmador confere o arquivo e deixa as listas "Kick · assistir" e "Twitch · chat"
// iguais à oficial. A lista "Kick · digitar" nunca é mexida.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type offcFile struct {
	Formato    string   `json:"formato"`
	Versao     int      `json:"versao"`
	Atualizado string   `json:"atualizado"`
	Twitch     []string `json:"twitch"`
	Kick       []string `json:"kick"`
}

var (
	offcMu      sync.Mutex
	offcMod     time.Time
	offcPending *offcFile
	offcNote    string    // aviso do que entrou/saiu
	offcNoteAt  time.Time // quando o aviso apareceu
	offcSeen    time.Time // última lista recebida do hub
)

func offcPath() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		return ""
	}
	return filepath.Join(base, "Farmador by oSirCheese", "lista-oficial.json")
}

// offcCheck roda no monitor: se o arquivo mudou, guarda a lista e pede para aplicar.
func offcCheck() {
	p := offcPath()
	if p == "" {
		return
	}
	st, err := os.Stat(p)
	if err != nil {
		return
	}
	offcMu.Lock()
	same := st.ModTime().Equal(offcMod)
	offcMu.Unlock()
	if same {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var f offcFile
	if json.Unmarshal(b, &f) != nil || f.Formato != "pokeidle-lista-oficial" || f.Versao != 1 {
		return
	}
	offcMu.Lock()
	offcMod = st.ModTime()
	offcPending = &f
	if t, err := time.Parse(time.RFC3339Nano, f.Atualizado); err == nil {
		offcSeen = t.Local()
	} else {
		offcSeen = st.ModTime()
	}
	offcMu.Unlock()
	pPostMessageW.Call(hwndMain, wmOffc, 0, 0)
}

func diffLists(old, nu []string) (add, rem []string) {
	o, n := map[string]bool{}, map[string]bool{}
	for _, x := range old {
		o[x] = true
	}
	for _, x := range nu {
		n[x] = true
		if !o[x] {
			add = append(add, x)
		}
	}
	for _, x := range old {
		if !n[x] {
			rem = append(rem, x)
		}
	}
	return
}

func describe(name string, add, rem []string) string {
	if len(add) == 0 && len(rem) == 0 {
		return ""
	}
	var parts []string
	for _, a := range add {
		parts = append(parts, "+"+a)
	}
	for _, r := range rem {
		parts = append(parts, "−"+r)
	}
	if len(parts) > 4 {
		parts = append(parts[:4], fmt.Sprintf("+%d", len(parts)-4))
	}
	return name + " " + strings.Join(parts, " ")
}

// offcApply roda na janela: troca as listas pela oficial e avisa no balão.
func offcApply() {
	offcMu.Lock()
	f := offcPending
	offcPending = nil
	offcMu.Unlock()
	if f == nil {
		return
	}
	kick := cleanList(f.Kick, true)
	tw := cleanList(f.Twitch, false)
	cfgMu.Lock()
	var notes []string
	twChanged, kickChanged := false, false
	if len(kick) >= 3 { // lista curta demais = leitura suspeita: não mexe
		add, rem := diffLists(cfg.KickWatch, kick)
		if d := describe("Kick", add, rem); d != "" {
			notes = append(notes, d)
			kickChanged = true
		}
		cfg.KickWatch = kick
	}
	if len(tw) >= 3 {
		add, rem := diffLists(cfg.TwChannels, tw)
		if d := describe("Twitch", add, rem); d != "" {
			notes = append(notes, d)
			twChanged = true
		}
		cfg.TwChannels = tw
	}
	kw, tc := strings.Join(cfg.KickWatch, "\r\n"), strings.Join(cfg.TwChannels, "\r\n")
	cfgMu.Unlock()
	if !twChanged && !kickChanged {
		return
	}
	saveConfig()
	offcMu.Lock()
	offcNote = strings.Join(notes, " · ")
	offcNoteAt = time.Now()
	offcMu.Unlock()
	if hwndSet != 0 { // configurações abertas: mostra a lista nova nos campos
		setText(edWatch, kw)
		setText(edTw, tc)
	}
	if twChanged {
		twApply()
	}
	wake()
	pPostMessageW.Call(hwndMain, wmKickSync, 0, 0)
	refreshMain()
}

// offcStatus: texto curto para as configurações e o aviso recente para o balão.
func offcStatus() (seen string, note string) {
	offcMu.Lock()
	defer offcMu.Unlock()
	if !offcSeen.IsZero() {
		seen = "lista oficial do hub: " + offcSeen.Format("02/01 15:04")
	}
	if offcNote != "" && time.Since(offcNoteAt) < 3*time.Hour {
		note = "Lista oficial " + offcNoteAt.Format("15:04") + ": " + offcNote
	}
	return
}
