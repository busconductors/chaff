# chaff

5-stage Lua obfuscator in Go. VX Underground SmartLoader parity.

## Quick start

```bash
go build -o bin/chaff ./cmd/chaff/
./bin/chaff --input payloads/hello.lua --output /tmp/hello-obf.lua --seed 42 --verbose
luajit /tmp/hello-obf.lua
```
