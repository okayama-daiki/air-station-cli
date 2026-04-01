package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"air-station-cli/internal/airstation"
)

type syncEntry struct {
	MAC string
	IP  string
}

type dhcpUpdate struct {
	Current airstation.DHCPAssignment
	NewIP   string
}

type syncDiff struct {
	MACsToAdd    []string
	MACsToRemove []string
	DHCPToAdd    []syncEntry
	DHCPToRemove []airstation.DHCPAssignment
	DHCPToUpdate []dhcpUpdate
}

func runSync(ctx context.Context, client *airstation.Client, csvPath string) error {
	entries, err := parseSyncCSV(csvPath)
	if err != nil {
		return fmt.Errorf("parsing CSV: %w", err)
	}
	fmt.Println("Reading current router configuration...")
	macState, err := client.ReadMacFiltering(ctx)
	if err != nil {
		return fmt.Errorf("reading MAC filter: %w", err)
	}
	dhcpState, err := client.ReadDHCPStaticAssignments(ctx)
	if err != nil {
		return fmt.Errorf("reading DHCP assignments: %w", err)
	}

	diff := computeSyncDiff(entries, macState, dhcpState)

	if !syncHasDiff(diff) {
		fmt.Println("No changes needed. Router is already in sync.")
		return nil
	}
	printSyncDiff(diff)

	if !syncConfirm("Apply these changes?") {
		fmt.Println("Aborted.")
		return nil
	}

	return applySyncDiff(ctx, client, diff)
}

func parseSyncCSV(path string) ([]syncEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	r.Comment = '#'

	var entries []syncEntry
	seen := make(map[string]bool)
	first := true
	lineNum := 1
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		if len(record) < 2 {
			lineNum++
			continue
		}
		mac := strings.TrimSpace(record[0])
		ip := strings.TrimSpace(record[1])

		// Skip header row
		if first {
			first = false
			if strings.EqualFold(mac, "mac") || strings.EqualFold(mac, "mac address") {
				lineNum++
				continue
			}
		}

		normMAC := airstation.NormalizeMAC(mac)
		if !airstation.IsMACAddress(normMAC) {
			return nil, fmt.Errorf("line %d: invalid MAC address: %q", lineNum, mac)
		}
		if !airstation.IsIPv4(ip) {
			return nil, fmt.Errorf("line %d: invalid IP address: %q", lineNum, ip)
		}
		if seen[normMAC] {
			return nil, fmt.Errorf("line %d: duplicate MAC address: %q", lineNum, mac)
		}
		seen[normMAC] = true

		entries = append(entries, syncEntry{
			MAC: normMAC,
			IP:  strings.TrimSpace(ip),
		})
		lineNum++
	}
	return entries, nil
}

func computeSyncDiff(entries []syncEntry, macState *airstation.MacFilterState, dhcpState []airstation.DHCPAssignment) syncDiff {
	csvByMAC := make(map[string]string)
	for _, e := range entries {
		csvByMAC[e.MAC] = e.IP
	}

	currentMACs := make(map[string]bool)
	for _, e := range macState.Entries {
		currentMACs[e.MAC] = true
	}

	// Only consider editable static DHCP reservations
	dhcpByMAC := make(map[string]airstation.DHCPAssignment)
	for _, e := range dhcpState {
		if e.EditTag != nil {
			dhcpByMAC[e.MAC] = e
		}
	}

	var diff syncDiff

	for mac := range csvByMAC {
		if !currentMACs[mac] {
			diff.MACsToAdd = append(diff.MACsToAdd, mac)
		}
	}
	for mac := range currentMACs {
		if _, inCSV := csvByMAC[mac]; !inCSV {
			diff.MACsToRemove = append(diff.MACsToRemove, mac)
		}
	}

	for mac, ip := range csvByMAC {
		if existing, ok := dhcpByMAC[mac]; ok {
			if existing.IP != ip {
				diff.DHCPToUpdate = append(diff.DHCPToUpdate, dhcpUpdate{Current: existing, NewIP: ip})
			}
		} else {
			diff.DHCPToAdd = append(diff.DHCPToAdd, syncEntry{MAC: mac, IP: ip})
		}
	}
	for mac, assignment := range dhcpByMAC {
		if _, inCSV := csvByMAC[mac]; !inCSV {
			if assignment.DeleteIndex != nil {
				diff.DHCPToRemove = append(diff.DHCPToRemove, assignment)
			}
		}
	}

	// Sort for deterministic output
	sort.Strings(diff.MACsToAdd)
	sort.Strings(diff.MACsToRemove)
	sort.Slice(diff.DHCPToAdd, func(i, j int) bool {
		return diff.DHCPToAdd[i].MAC < diff.DHCPToAdd[j].MAC
	})
	sort.Slice(diff.DHCPToRemove, func(i, j int) bool {
		return diff.DHCPToRemove[i].MAC < diff.DHCPToRemove[j].MAC
	})
	sort.Slice(diff.DHCPToUpdate, func(i, j int) bool {
		return diff.DHCPToUpdate[i].Current.MAC < diff.DHCPToUpdate[j].Current.MAC
	})

	return diff
}

func syncHasDiff(diff syncDiff) bool {
	return len(diff.MACsToAdd) > 0 || len(diff.MACsToRemove) > 0 ||
		len(diff.DHCPToAdd) > 0 || len(diff.DHCPToRemove) > 0 ||
		len(diff.DHCPToUpdate) > 0
}

func printSyncDiff(diff syncDiff) {
	fmt.Println("Changes to apply:")
	fmt.Println()

	if len(diff.MACsToAdd) > 0 || len(diff.MACsToRemove) > 0 {
		fmt.Println("MAC Filter:")
		for _, mac := range diff.MACsToAdd {
			fmt.Printf("  + %s\n", mac)
		}
		for _, mac := range diff.MACsToRemove {
			fmt.Printf("  - %s\n", mac)
		}
		fmt.Println()
	}

	if len(diff.DHCPToAdd) > 0 || len(diff.DHCPToRemove) > 0 || len(diff.DHCPToUpdate) > 0 {
		fmt.Println("DHCP Reservations:")
		for _, e := range diff.DHCPToAdd {
			fmt.Printf("  + %s  %s\n", e.MAC, e.IP)
		}
		for _, a := range diff.DHCPToRemove {
			fmt.Printf("  - %s  %s\n", a.MAC, a.IP)
		}
		for _, u := range diff.DHCPToUpdate {
			fmt.Printf("  ~ %s  %s -> %s\n", u.Current.MAC, u.Current.IP, u.NewIP)
		}
		fmt.Println()
	}
}

func syncConfirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(scanner.Text())
		return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
	}
	return false
}

func applySyncDiff(ctx context.Context, client *airstation.Client, diff syncDiff) error {
	// Delete before add/update to avoid capacity issues
	for _, mac := range diff.MACsToRemove {
		fmt.Printf("Removing MAC filter: %s\n", mac)
		if _, err := client.RemoveMAC(ctx, mac); err != nil {
			return fmt.Errorf("removing MAC %s: %w", mac, err)
		}
	}
	for _, mac := range diff.MACsToAdd {
		fmt.Printf("Adding MAC filter: %s\n", mac)
		if _, err := client.AddMAC(ctx, mac); err != nil {
			return fmt.Errorf("adding MAC %s: %w", mac, err)
		}
	}

	// Delete, then update, then add DHCP reservations
	for _, a := range diff.DHCPToRemove {
		fmt.Printf("Removing DHCP reservation: %s  %s\n", a.MAC, a.IP)
		if _, err := client.RemoveDHCPStaticAssignment(ctx, a.MAC); err != nil {
			return fmt.Errorf("removing DHCP %s: %w", a.MAC, err)
		}
	}
	for _, u := range diff.DHCPToUpdate {
		fmt.Printf("Updating DHCP reservation: %s  %s -> %s\n", u.Current.MAC, u.Current.IP, u.NewIP)
		if _, err := client.UpdateDHCPStaticAssignment(ctx, u.Current.MAC, u.NewIP, ""); err != nil {
			return fmt.Errorf("updating DHCP %s: %w", u.Current.MAC, err)
		}
	}
	for _, e := range diff.DHCPToAdd {
		fmt.Printf("Adding DHCP reservation: %s  %s\n", e.MAC, e.IP)
		if _, err := client.AddDHCPStaticAssignment(ctx, e.IP, e.MAC); err != nil {
			return fmt.Errorf("adding DHCP %s %s: %w", e.MAC, e.IP, err)
		}
	}

	fmt.Println("Sync complete.")
	return nil
}
