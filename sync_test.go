package main

import (
	"os"
	"testing"

	"air-station-cli/internal/airstation"
)

func TestParseSyncCSV(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		want      []syncEntry
		wantError bool
	}{
		{
			name:    "valid CSV with header",
			content: "IP address,MAC address,Owner,Affirmation,Note\n192.168.1.100,00:11:22:33:44:55,,,\n192.168.1.101,AA:BB:CC:DD:EE:FF,,,\n",
			want: []syncEntry{
				{MAC: "00:11:22:33:44:55", IP: "192.168.1.100"},
				{MAC: "AA:BB:CC:DD:EE:FF", IP: "192.168.1.101"},
			},
			wantError: false,
		},
		{
			name:    "valid CSV without header",
			content: "192.168.1.100,00:11:22:33:44:55\n192.168.1.101,AA:BB:CC:DD:EE:FF\n",
			want: []syncEntry{
				{MAC: "00:11:22:33:44:55", IP: "192.168.1.100"},
				{MAC: "AA:BB:CC:DD:EE:FF", IP: "192.168.1.101"},
			},
			wantError: false,
		},
		{
			name:      "invalid MAC address",
			content:   "192.168.1.100,00:11:22:33:44:ZZ\n",
			want:      nil,
			wantError: true,
		},
		{
			name:      "invalid IP address",
			content:   "999.999.999.999,00:11:22:33:44:55\n",
			want:      nil,
			wantError: true,
		},
		{
			name:      "duplicate MAC",
			content:   "192.168.1.100,00:11:22:33:44:55\n192.168.1.101,00:11:22:33:44:55\n",
			want:      nil,
			wantError: true,
		},
		{
			name:    "comments and blank lines",
			content: "# This is a comment\nIP address,MAC address\n\n192.168.1.100,00:11:22:33:44:55\n# Another comment\n",
			want: []syncEntry{
				{MAC: "00:11:22:33:44:55", IP: "192.168.1.100"},
			},
			wantError: false,
		},
		{
			name:    "insensitive header matching",
			content: "ip address,MAC address\n192.168.1.100,00:11:22:33:44:55\n",
			want: []syncEntry{
				{MAC: "00:11:22:33:44:55", IP: "192.168.1.100"},
			},
			wantError: false,
		},
		{
			name:    "skip empty MAC row",
			content: "IP address,MAC address,Owner,Affirmation,Note\n192.168.11.1,,,,ルータ\n192.168.1.100,00:11:22:33:44:55,,,\n",
			want: []syncEntry{
				{MAC: "00:11:22:33:44:55", IP: "192.168.1.100"},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary CSV file
			tmpfile, err := os.CreateTemp("", "sync_test_*.csv")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.WriteString(tt.content); err != nil {
				t.Fatalf("failed to write temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("failed to close temp file: %v", err)
			}

			got, err := parseSyncCSV(tmpfile.Name())
			if (err != nil) != tt.wantError {
				t.Errorf("parseSyncCSV() error = %v, wantError %v", err, tt.wantError)
				return
			}
			if err != nil {
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("parseSyncCSV() got %d entries, want %d", len(got), len(tt.want))
				return
			}

			for i, entry := range got {
				if entry.MAC != tt.want[i].MAC || entry.IP != tt.want[i].IP {
					t.Errorf("parseSyncCSV() entry %d = {%s, %s}, want {%s, %s}",
						i, entry.MAC, entry.IP, tt.want[i].MAC, tt.want[i].IP)
				}
			}
		})
	}
}

func TestComputeSyncDiff(t *testing.T) {
	tests := []struct {
		name     string
		entries  []syncEntry
		macState *airstation.MacFilterState
		dhcpState []airstation.DHCPAssignment
		check    func(diff syncDiff) bool
	}{
		{
			name:    "no changes needed",
			entries: []syncEntry{{MAC: "00:11:22:33:44:55", IP: "192.168.1.100"}},
			macState: &airstation.MacFilterState{
				Entries: []airstation.MacEntry{{MAC: "00:11:22:33:44:55"}},
			},
			dhcpState: []airstation.DHCPAssignment{
				{MAC: "00:11:22:33:44:55", IP: "192.168.1.100", EditTag: &[]int{1}[0], DeleteIndex: &[]int{1}[0]},
			},
			check: func(diff syncDiff) bool {
				return len(diff.MACsToAdd) == 0 && len(diff.MACsToRemove) == 0 &&
					len(diff.DHCPToAdd) == 0 && len(diff.DHCPToRemove) == 0 && len(diff.DHCPToUpdate) == 0
			},
		},
		{
			name:    "add MAC and DHCP",
			entries: []syncEntry{{MAC: "00:11:22:33:44:55", IP: "192.168.1.100"}},
			macState: &airstation.MacFilterState{
				Entries: []airstation.MacEntry{},
			},
			dhcpState: []airstation.DHCPAssignment{},
			check: func(diff syncDiff) bool {
				return len(diff.MACsToAdd) == 1 && diff.MACsToAdd[0] == "00:11:22:33:44:55" &&
					len(diff.DHCPToAdd) == 1 && diff.DHCPToAdd[0].MAC == "00:11:22:33:44:55"
			},
		},
		{
			name:    "remove MAC and DHCP",
			entries: []syncEntry{},
			macState: &airstation.MacFilterState{
				Entries: []airstation.MacEntry{{MAC: "00:11:22:33:44:55"}},
			},
			dhcpState: []airstation.DHCPAssignment{
				{MAC: "00:11:22:33:44:55", IP: "192.168.1.100", EditTag: &[]int{1}[0], DeleteIndex: &[]int{1}[0]},
			},
			check: func(diff syncDiff) bool {
				return len(diff.MACsToRemove) == 1 && diff.MACsToRemove[0] == "00:11:22:33:44:55" &&
					len(diff.DHCPToRemove) == 1 && diff.DHCPToRemove[0].MAC == "00:11:22:33:44:55"
			},
		},
		{
			name:    "skip non-deletable DHCP entries",
			entries: []syncEntry{},
			macState: &airstation.MacFilterState{
				Entries: []airstation.MacEntry{},
			},
			dhcpState: []airstation.DHCPAssignment{
				{MAC: "00:11:22:33:44:55", IP: "192.168.1.100", EditTag: &[]int{1}[0], DeleteIndex: nil},
			},
			check: func(diff syncDiff) bool {
				return len(diff.DHCPToRemove) == 0
			},
		},
		{
			name:    "deterministic sort order",
			entries: []syncEntry{{MAC: "cc:cc:cc:cc:cc:cc", IP: "192.168.1.103"}, {MAC: "aa:aa:aa:aa:aa:aa", IP: "192.168.1.101"}, {MAC: "bb:bb:bb:bb:bb:bb", IP: "192.168.1.102"}},
			macState: &airstation.MacFilterState{
				Entries: []airstation.MacEntry{
					{MAC: "cc:cc:cc:cc:cc:cc"},
					{MAC: "aa:aa:aa:aa:aa:aa"},
					{MAC: "bb:bb:bb:bb:bb:bb"},
				},
			},
			dhcpState: []airstation.DHCPAssignment{
				{MAC: "cc:cc:cc:cc:cc:cc", IP: "192.168.1.103", EditTag: &[]int{3}[0]},
				{MAC: "aa:aa:aa:aa:aa:aa", IP: "192.168.1.101", EditTag: &[]int{1}[0]},
				{MAC: "bb:bb:bb:bb:bb:bb", IP: "192.168.1.102", EditTag: &[]int{2}[0]},
			},
			check: func(diff syncDiff) bool {
				// Verify sorted order
				if len(diff.DHCPToAdd) > 0 {
					for i := 1; i < len(diff.DHCPToAdd); i++ {
						if diff.DHCPToAdd[i].MAC < diff.DHCPToAdd[i-1].MAC {
							return false
						}
					}
				}
				return true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := computeSyncDiff(tt.entries, tt.macState, tt.dhcpState)
			if !tt.check(diff) {
				t.Errorf("computeSyncDiff() check failed")
			}
		})
	}
}
