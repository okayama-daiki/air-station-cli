# air-station-cli

A simple CLI to control Buffalo AirStation router via its web interface.

Current scope:

- MAC address filtering
- DHCP static assignments

Default target: `http://192.168.11.1` / `admin`. Password must be supplied via `--password` (or a shell alias).

```bash
# ~/.zshrc
alias air-station='/path/to/air-station --password <your-password>'
```

## Build

```bash
go build -o air-station .
```

Or run directly:

```bash
go run . --help
```

## Usage

```bash
./air-station mac show
./air-station mac set --enable-2g on --enable-5g off
./air-station mac add AA:BB:CC:DD:EE:FF
./air-station mac update AA:BB:CC:DD:EE:FF 11:22:33:44:55:66
./air-station mac remove AA:BB:CC:DD:EE:FF

./air-station dhcp show
./air-station dhcp add --ip 192.168.11.200 --mac AA:BB:CC:DD:EE:FF
./air-station dhcp update AA:BB:CC:DD:EE:FF --ip 192.168.11.210
./air-station dhcp remove 192.168.11.210
```

JSON output:

```bash
./air-station mac show --json
./air-station dhcp show --json
```

Options:

```bash
--base-url <url>   # default: http://192.168.11.1
--username <name>  # default: admin
--password <pass>
```
