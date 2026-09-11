@echo off
set GOARCH=386
set GOOS=windows

goversioninfo
go build -ldflags="-H windowsgui" -o "FactoryTalk View XML konverter SE do ME.exe"