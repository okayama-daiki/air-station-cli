package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"air-station-cli/internal/airstation"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
	if len(positionals) < 1 {
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
	} else if value := os.Getenv("AIR_STATION_PASSWORD"); value != "" {
		cfg.Password = value
	}

	client, err := airstation.NewClient(cfg)
	if err != nil {
		return err
	}
	defer client.Logout(ctx)

	resource := positionals[0]
	jsonOutput := options["json"] == "true"

	switch resource {
	case "sync":
		if len(positionals) < 2 {
			return errors.New("missing CSV file path")
		}
		return runSync(ctx, client, positionals[1])
	case "mac":
		if len(positionals) < 2 {
			printHelp()
			return errors.New("missing mac action")
		}
		return runMAC(ctx, client, positionals[1], positionals[2:], options, jsonOutput)
	case "dhcp":
		if len(positionals) < 2 {
			printHelp()
			return errors.New("missing dhcp action")
		}
		return runDHCP(ctx, client, positionals[1], positionals[2:], options, jsonOutput)
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
		if err == nil && !jsonOutput {
			fmt.Println("Updated")
		}
	case "add":
		if len(args) < 1 {
			return errors.New("missing MAC address")
		}
		if err = client.AddMAC(ctx, args[0]); err == nil {
			if !jsonOutput {
				fmt.Printf("Added %s\n", args[0])
			}
			result, err = client.ReadMacFiltering(ctx)
		}
	case "update":
		if len(args) < 2 {
			return errors.New("missing current/new MAC address")
		}
		if !jsonOutput {
			fmt.Printf("Updated %s -> %s\n", args[0], args[1])
		}
		result, err = client.UpdateMAC(ctx, args[0], args[1])
	case "remove":
		if len(args) < 1 {
			return errors.New("missing MAC address")
		}
		if err = client.RemoveMAC(ctx, args[0]); err == nil {
			if !jsonOutput {
				fmt.Printf("Removed %s\n", args[0])
			}
			result, err = client.ReadMacFiltering(ctx)
		}
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
		ip, err1 := requireOption(options, "ip")
		if err1 != nil {
			return err1
		}
		mac, err2 := requireOption(options, "mac")
		if err2 != nil {
			return err2
		}
		result, err = client.AddDHCPStaticAssignment(ctx, ip, mac)
		if err == nil && !jsonOutput {
			fmt.Printf("Added %s  %s\n", mac, ip)
		}
	case "update":
		if len(args) < 1 {
			return errors.New("missing selector for dhcp update")
		}
		if options["ip"] == "" && options["mac"] == "" {
			return errors.New("specify --ip and/or --mac")
		}
		result, err = client.UpdateDHCPStaticAssignment(ctx, args[0], options["ip"], options["mac"])
		if err == nil && !jsonOutput {
			fmt.Printf("Updated %s\n", args[0])
		}
	case "remove":
		if len(args) < 1 {
			return errors.New("missing selector for dhcp remove")
		}
		result, err = client.RemoveDHCPStaticAssignment(ctx, args[0])
		if err == nil && !jsonOutput {
			fmt.Printf("Removed %s\n", args[0])
		}
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
		if token == "--json" {
			options["json"] = "true"
			continue
		}
		if len(token) < 3 || token[:2] != "--" {
			positionals = append(positionals, token)
			continue
		}
		if index+1 >= len(argv) || strings.HasPrefix(argv[index+1], "--") {
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
	cfg := airstation.DefaultConfig()
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

  air-station sync <csv-file>

    Sync MAC filter and DHCP reservations from a CSV file.
    CSV format: MAC,IP (one entry per line, header row optional).
    Shows a diff and prompts for confirmation before applying changes.

Options:
  --base-url <url>    Default: %s
  --username <name>   Default: %s
  --password <pass>   Default: AIR_STATION_PASSWORD or empty
  --json              Print raw JSON
  -h, --help          Show help
`, cfg.BaseURL, cfg.Username)
}
