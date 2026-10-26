package watch

import (
	"slices"
	"testing"
)

func TestRuleOut(t *testing.T) {
	got := ruleOut(
		[]string{"actieve luidsprekers", "passief", "luidspreker", "Bluetooth", "bluetooth", "ingebouwde versterker"},
		[]string{"passieve luidsprekers", "veerklemmen"}, "passieve luidsprekers", "luidsprekers")
	want := []string{"actieve luidsprekers", "Bluetooth", "ingebouwde versterker"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestVariant(t *testing.T) {
	a := wishWords("passieve luidsprekers met klemmen")
	if !variant(a, wishWords("passieve luidsprekers met standaard klemmen")) {
		t.Error("a reading with one more word is the same reading")
	}
	if variant(a, wishWords("actieve luidsprekers")) {
		t.Error("different kinds are different readings")
	}
}

func TestUsageSnippet(t *testing.T) {
	text := "Type: 2-weg basreflex speakers. Aansluiting: standaard veerklemmen (rood/zwart) voor gewone luidsprekerkabel."
	snippet, n := around(text, wishWords("rood-zwarte draad"))
	if n != 2 || snippet == "" {
		t.Errorf("snippet %q matched %d words, want rood and zwart", snippet, n)
	}
}
