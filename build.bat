@echo off
rem Compila o Farmador de pontos (precisa do Go 1.24+: https://go.dev/dl)
go build -trimpath -ldflags "-H windowsgui -s -w" -o "Farmador de pontos.exe" .
if errorlevel 1 pause
