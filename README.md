# Farmador de pontos

App para Windows (um único `.exe`, feito em Go) que acompanha lives da **Kick** e da **Twitch** e junta os pontos de canal de uma conta em cada plataforma.

- Abre as lives em páginas escondidas (WebView2) com a conta 01, que você liga e pausa pelo botão Ligar/Pausar.
- Mostra os pontos de cada canal e a soma, com cor por faixa: vermelho de 200 a 499, azul de 500 a 999 e amarelo a partir de 1000.
- Clicar num canal abre a live para resgatar.
- Pode mandar frases no chat da Kick, **só** em canais de streamers que permitiram, pela API oficial da Kick.
- Medidor de memória/CPU ("PESO AGORA").
- Atualização automática: a cada 2 min confere o Release mais novo deste repositório e se instala sozinho. Um zip `farmador-osircheese-vX.Y.Z.zip` mais novo em Downloads ou na Área de Trabalho também é instalado sozinho.

## Publicar uma versão nova

1. Suba `appVersion` em `main.go` e compile.
2. Zipe o `.exe` como `farmador-osircheese-vX.Y.Z.zip`.
3. `gh release create vX.Y.Z farmador-osircheese-vX.Y.Z.zip --title "vX.Y.Z"`: em até 2 min os Farmadores abertos se atualizam.

## Requisitos

- Windows 10 ou 11 com o [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/) (já vem no Windows 11).
- Para compilar: [Go 1.24+](https://go.dev/dl).

## Compilar

No Windows, rode `build.bat`. De outro sistema:

```
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H windowsgui -s -w" -o "Farmador de pontos.exe" .
```

O ícone e o manifesto já estão em `rsrc_windows_amd64.syso`. Para refazê-los: `python icon/mksyso.py icon/icons.pkl icon/manifest.xml rsrc_windows_amd64.syso`.

## Configuração

Tudo fica em `kick-timer.json`, ao lado do `.exe`. Esse arquivo guarda o Client Secret da Kick e os acessos das contas, então **nunca o publique** (ele já está no `.gitignore`).

- **Kick (digitar):** crie um app em kick.com/settings/developer e preencha o Client ID e o Client Secret em Configurações → Contas.
- **Twitch (lives ao vivo):** use "Autorizar (lives ao vivo)" em Configurações → Contas.

## Arquivos

| Arquivo | O que faz |
|---|---|
| `main.go` | balão principal, desenho e cliques |
| `settings.go` | janela de configurações (Contas, Canais, Frases) |
| `kickweb.go` | páginas escondidas da Kick e da Twitch (WebView2) |
| `profiles.go` | um perfil de navegador por conta |
| `kick.go` | configuração e API oficial da Kick |
| `twitch.go` | API da Twitch (lives ao vivo, login) |
| `monitor.go` | verificação periódica de quem está ao vivo |
| `update.go` | atualização automática e instância única |
| `weight.go` | medidor de memória e CPU |

Desenvolvido por oSirCheese.
