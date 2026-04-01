package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"air-station-cli/internal/airstation"
)

func main() {
	_ = loadDotEnv(".env")
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loadDotEnv(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if os.Getenv(key) == "" {
			os.Setenv(key, strings.TrimSpace(value))
		}
	}
	return nil
}

func run(ctx context.Context, argv []string) error {
	if len(argv) == 0 || hasHelpFlag(argv) {
		printHelp()
		return nil
	}

	positionals, options, err := parseArgs(argv)
	if err != nil {
		return err
	}
	if len(positionals) < 2 {
		printHelp()
		return errors.New("resource and action are required")
	}

	cfg := airstation.DefaultConfig()
	if value := options["base-url"]; value != "" {
		cfg.BaseURL = value
	}
	if value := options["username"]; value != "" {
		cfg.Username = value
	}
	if value := options["password"]; value != "" {
		cfg.Password = value
	}
	cfg.Timeout = 15 * time.Second

	client, err := airstation.NewClient(cfg)
	if err != nil {
		return err
	}

	resource, action := positionals[0], positionals[1]
	jsonOutput := options["json"] == "true"

	switch resource {
	case "mac":
		return runMAC(ctx, client, action, positionals[2:], options, jsonOutput)
	case "dhcp":
		return runDHCP(ctx, client, action, positionals[2:], options, jsonOutput)
	default:
		return fmt.Errorf("unknown resource: %s", resource)
	}
}

func runMAC(ctx context.Context, client *airstation.Client, action string, args []string, options map[string]string, jsonOutput bool) error {
	var (
		result *airstation.MacFilterState
		err    error
	)

	switch action {
	case "show":
		result, err = client.ReadMacFiltering(ctx)
	case "set":
		enable2g, err2 := parseOptionalToggle(options, "enable-2g")
		if err2 != nil {
			return err2
		}
		enable5g, err5 := parseOptionalToggle(options, "enable-5g")
		if err5 != nil {
			return err5
		}
		if enable2g == nil && enable5g == nil {
			return errors.New("specify --enable-2g and/or --enable-5g")
		}
		result, err = client.SetMacFiltering(ctx, enable2g, enable5g)
	case "add":
		if len(args) < 1 {
			return errors.New("missing MAC address")
		}
		result, err = client.AddMAC(ctx, args[0])
	case "update":
		if len(args) < 2 {
			return errors.New("missing current/new MAC address")
		}
		result, err = client.UpdateMAC(ctx, args[0], args[1])
	case "remove":
		if len(args) < 1 {
			return errors.New("missing MAC address")
		}
		result, err = client.RemoveMAC(ctx, args[0])
	default:
		return fmt.Errorf("unknown mac action: %s", action)
	}
	if err != nil {
		return err
	}

	if jsonOutput {
		return printJSON(result)
	}
	fmt.Println(formatMACState(result))
	return nil
}

func runDHCP(ctx context.Context, client *airstation.Client, action string, args []string, options map[string]string, jsonOutput bool) error {
	var (
		result []airstation.DHCPAssignment
		err    error
	)

	switch action {
	case "show":
		result, err = client.ReadDHCPStaticAssignments(ctx)
	case "add":
		ip, err := requireOption(options, "ip")
		if err != nil {
			return err
		}
		mac, err := requireOption(options, "mac")
		if err != nil {
			return err
		}
		result, err = client.AddDHCPStaticAssignment(ctx, ip, mac)
	case "update":
		if len(args) < 1 {
			return errors.New("missing selector for dhcp update")
		}
		if options["ip"] == "" && options["mac"] == "" {
			return errors.New("specify --ip and/or --mac")
		}
		result, err = client.UpdateDHCPStaticAssignment(ctx, args[0], options["ip"], options["mac"])
	case "remove":
		if len(args) < 1 {
			return errors.New("missing selector for dhcp remove")
		}
		result, err = client.RemoveDHCPStaticAssignment(ctx, args[0])
	default:
		return fmt.Errorf("unknown dhcp action: %s", action)
	}
	if err != nil {
		return err
	}

	if jsonOutput {
		return printJSON(result)
	}
	fmt.Println(formatDHCPState(result))
	return nil
}

func parseArgs(argv []string) ([]string, map[string]string, error) {
	positionals := make([]string, 0)
	options := make(map[string]string)

	for index := 0; index < len(argv); index++ {
		token := argv[index]
		if token == "-h" || token == "--help" {
			options["help"] = "true"
			continue
		}
		if token == "--json" {
			options["json"] = "true"
			continue
		}
		if len(token) < 3 || token[:2] != "--" {
			positionals = append(positionals, token)
			continue
		}
		if index+1 >= len(argv) || len(argv[index+1]) >= 2 && argv[index+1][:2] == "--" {
			return nil, nil, fmt.Errorf("missing value for %s", token)
		}
		options[token[2:]] = argv[index+1]
		index++
	}

	return positionals, options, nil
}

func hasHelpFlag(argv []string) bool {
	for _, token := range argv {
		if token == "-h" || token == "--help" {
			return true
		}
	}
	return false
}

func parseOptionalToggle(options map[string]string, key string) (*bool, error) {
	value := options[key]
	if value == "" {
		return nil, nil
	}
	switch value {
	case "on":
		result := true
		return &result, nil
	case "off":
		result := false
		return &result, nil
	default:
		return nil, fmt.Errorf("%s must be on or off", key)
	}
}

func requireOption(options map[string]string, key string) (string, error) {
	value := options[key]
	if value == "" {
		return "", fmt.Errorf("missing required option --%s", key)
	}
	return value, nil
}

func printJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func formatMACState(state *airstation.MacFilterState) string {
	lines := []string{
		fmt.Sprintf("2.4GHz filtering: %s", onOff(state.Enabled24)),
		fmt.Sprintf("5GHz filtering: %s", onOff(state.Enabled5)),
		"",
	}
	for _, entry := range state.Entries {
		status := "not-connected"
		if entry.Connected {
			status = "connected"
		}
		lines = append(lines, fmt.Sprintf("%s  %s", entry.MAC, status))
	}
	return strings.Join(lines, "\n")
}

func formatDHCPState(entries []airstation.DHCPAssignment) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		suffix := ""
		if entry.CurrentClient {
			suffix = "  current-client"
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s%s", entry.IP, entry.MAC, entry.Status, entry.Lease, suffix))
	}
	return strings.Join(lines, "\n")
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func printHelp() {
	fmt.Printf(`air-station

Usage:
  air-station mac show [--json]
  air-station mac set [--enable-2g on|off] [--enable-5g on|off] [--json]
  air-station mac add <mac> [--json]
  air-station mac update <current-mac> <new-mac> [--json]
  air-station mac remove <mac> [--json]

  air-station dhcp show [--json]
  air-station dhcp add --ip <ip> --mac <mac> [--json]
  air-station dhcp update <ip-or-mac> [--ip <ip>] [--mac <mac>] [--json]
  air-station dhcp remove <ip-or-mac> [--json]

Options:
  --base-url <url>    Default: %s
  --username <name>   Default: %s
  --password <pass>   Default: configured in source
  --json              Print raw JSON
  -h, --help          Show help
`, airstation.DefaultConfig().BaseURL, airstation.DefaultConfig().Username)
}
