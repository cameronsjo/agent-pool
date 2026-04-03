package main

import (
	"testing"
)

func TestParseFlagsFromArgs_AllPresent(t *testing.T) {
	args := []string{"--pool", "/tmp/pool", "--expert", "auth"}
	result := parseFlagsFromArgs(args, "pool", "expert")

	if result["pool"] != "/tmp/pool" {
		t.Errorf("pool = %q, want %q", result["pool"], "/tmp/pool")
	}
	if result["expert"] != "auth" {
		t.Errorf("expert = %q, want %q", result["expert"], "auth")
	}
}

func TestParseFlagsFromArgs_SomeMissing(t *testing.T) {
	args := []string{"--pool", "/tmp/pool"}
	result := parseFlagsFromArgs(args, "pool", "expert")

	if result["pool"] != "/tmp/pool" {
		t.Errorf("pool = %q, want %q", result["pool"], "/tmp/pool")
	}
	if result["expert"] != "" {
		t.Errorf("expert = %q, want empty string", result["expert"])
	}
}

func TestParseFlagsFromArgs_EmptyArgs(t *testing.T) {
	result := parseFlagsFromArgs(nil, "pool", "expert")

	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestParseFlagsFromArgs_ThreeFlags(t *testing.T) {
	args := []string{"--pool", "/tmp/pool", "--expert", "auth", "--task", "task-001"}
	result := parseFlagsFromArgs(args, "pool", "expert", "task")

	if result["pool"] != "/tmp/pool" {
		t.Errorf("pool = %q, want %q", result["pool"], "/tmp/pool")
	}
	if result["expert"] != "auth" {
		t.Errorf("expert = %q, want %q", result["expert"], "auth")
	}
	if result["task"] != "task-001" {
		t.Errorf("task = %q, want %q", result["task"], "task-001")
	}
}

func TestParseFlagsFromArgs_UnknownFlagsIgnored(t *testing.T) {
	args := []string{"--unknown", "value", "--pool", "/tmp/pool"}
	result := parseFlagsFromArgs(args, "pool")

	if result["pool"] != "/tmp/pool" {
		t.Errorf("pool = %q, want %q", result["pool"], "/tmp/pool")
	}
	if _, ok := result["unknown"]; ok {
		t.Error("unexpected 'unknown' key in result")
	}
}

func TestParseFlagsFromArgs_FlagAtEnd(t *testing.T) {
	// Flag name at the end with no value should be ignored
	args := []string{"--pool"}
	result := parseFlagsFromArgs(args, "pool")

	if result["pool"] != "" {
		t.Errorf("pool = %q, want empty (no value after flag)", result["pool"])
	}
}

func TestParseFlagsFromArgs_RepeatedFlag(t *testing.T) {
	// Last value wins
	args := []string{"--pool", "first", "--pool", "second"}
	result := parseFlagsFromArgs(args, "pool")

	if result["pool"] != "second" {
		t.Errorf("pool = %q, want %q (last value wins)", result["pool"], "second")
	}
}
