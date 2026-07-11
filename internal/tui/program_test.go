package tui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func TestTeatestKeyboardResizeDisconnectReconnectFlow(t *testing.T) {
	domain := smallFixtureModel(Capabilities{})
	domain.Polling = false
	domain.FocusIndex = 2
	domain = preserveFocus(domain)
	testModel := teatest.NewTestModel(t, NewTestProgramModel(domain), teatest.WithInitialTermSize(80, 24))

	testModel.Send(tea.KeyMsg{Type: tea.KeyTab})
	testModel.Send(tea.KeyMsg{Type: tea.KeyDown})
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	testModel.Send(tea.KeyMsg{Type: tea.KeyCtrlK})
	testModel.Type("work")
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	testModel.Send(tea.KeyMsg{Type: tea.KeyPgDown})
	testModel.Send(tea.KeyMsg{Type: tea.KeyPgUp})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyShiftTab}, {Type: tea.KeyUp}, {Type: tea.KeyDown},
		{Type: tea.KeyLeft}, {Type: tea.KeyRight},
		{Type: tea.KeyRunes, Runes: []rune{'h'}}, {Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyRunes, Runes: []rune{'k'}}, {Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeySpace}, {Type: tea.KeyPgUp}, {Type: tea.KeyPgDown},
		{Type: tea.KeyHome}, {Type: tea.KeyEnd},
		{Type: tea.KeyRunes, Runes: []rune{'['}}, {Type: tea.KeyRunes, Runes: []rune{']'}},
		{Type: tea.KeyRunes, Runes: []rune{'p'}}, {Type: tea.KeyRunes, Runes: []rune{'p'}},
	} {
		testModel.Send(key)
	}
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	testModel.Type("status:blocked")
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool {
		return bytes.Contains(output, []byte("status:blocked"))
	}, teatest.WithDuration(2*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	for i := 0; i < 120; i++ {
		width := 60 + i%120
		height := 16 + i%35
		testModel.Send(tea.WindowSizeMsg{Width: width, Height: height})
	}
	testModel.Send(tea.WindowSizeMsg{Width: 120, Height: 30})

	failedAt := fixtureTime.Add(time.Minute)
	testModel.Send(refreshBatch{messages: []Msg{
		SnapshotError{Source: daemonclient.SourceInstances, Error: "connection refused", At: failedAt},
		SnapshotError{Source: daemonclient.SourceJobs, Error: "connection refused", At: failedAt},
		SnapshotError{Source: daemonclient.SourceTopology, Error: "connection refused", At: failedAt},
		SnapshotError{Source: daemonclient.SourceResources, Error: "connection refused", At: failedAt},
		RefreshFinished{At: failedAt, Error: "connection refused"},
	}})
	reconnectedAt := failedAt.Add(time.Second)
	messages := []Msg{}
	for _, source := range daemonclient.SnapshotSources() {
		messages = append(messages, SnapshotOK{Source: source, Snapshot: smallFixtureSnapshot(), At: reconnectedAt})
	}
	messages = append(messages, RefreshFinished{At: reconnectedAt, AnySuccess: true, Complete: true})
	testModel.Send(refreshBatch{messages: messages})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	testModel.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	final := testModel.FinalModel(t, teatest.WithFinalTimeout(3*time.Second))
	program, ok := final.(ProgramModel)
	if !ok {
		t.Fatalf("final model = %T", final)
	}
	if program.Domain.Connection != ConnectionReconnected || program.Domain.Width != 120 || program.Domain.Height != 30 {
		t.Fatalf("final domain = %+v", program.Domain)
	}
	if program.Domain.Query != "status:blocked" || program.Domain.QueryError != "" {
		t.Fatalf("query = %q error=%q", program.Domain.Query, program.Domain.QueryError)
	}
	output, err := io.ReadAll(testModel.FinalOutput(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output, []byte("agent-team")) || !bytes.Contains(output, []byte("status:blocked")) {
		t.Fatalf("teatest output missing shell/query: %q", output)
	}
}

func TestTeatestTermDumbKeyboardCaptureHasNoControlBytes(t *testing.T) {
	domain := smallFixtureModel(Capabilities{Dumb: true})
	domain.Polling = false
	var plain bytes.Buffer
	program := NewTestProgramModel(domain)
	program.plainOutput = &plain
	testModel := teatest.NewTestModel(t, program, teatest.WithInitialTermSize(80, 24))
	sequence := []tea.KeyMsg{
		{Type: tea.KeyTab},
		{Type: tea.KeyDown},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'/'}},
	}
	for _, key := range sequence {
		testModel.Send(key)
	}
	testModel.Type("status:active")
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'r'}},
		{Type: tea.KeyRunes, Runes: []rune{'?'}},
		{Type: tea.KeyRunes, Runes: []rune{'?'}},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
	} {
		testModel.Send(key)
	}
	testModel.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	bytes := plain.Bytes()
	for index, value := range bytes {
		if value == 0x1b || value == 0x9b || value == 0x9d {
			t.Fatalf("plain TERM=dumb capture has control byte %#x at %d", value, index)
		}
	}
	for _, text := range []string{"agent-team", "status:active", "Help"} {
		if !strings.Contains(plain.String(), text) {
			t.Errorf("plain capture missing %q", text)
		}
	}
	lines := strings.Split(strings.TrimSuffix(plain.String(), "\n"), "\n")
	if len(lines)%24 != 0 {
		t.Fatalf("plain capture contains an interleaved/partial frame: %d lines is not a multiple of 24", len(lines))
	}
}

func TestPTYBindingRegistrySweepChangesIntendedState(t *testing.T) {
	for _, binding := range Bindings() {
		binding := binding
		t.Run(binding.ID, func(t *testing.T) {
			domain := smallFixtureModel(Capabilities{Dumb: true})
			domain.RefreshInFlight = false
			if binding.ID == "escape" {
				domain.Query = "status:blocked"
			}
			if binding.ID == "move" || binding.ID == "page" {
				domain.FocusIndex = 2
				domain = preserveFocus(domain)
				domain.Focus.ItemID = projectOverview(domain).Attention[1].ID
			}
			if binding.ID == "go" {
				domain.Route = RouteWork
			}
			before := domain
			testModel := teatest.NewTestModel(t, NewTestProgramModel(domain), teatest.WithInitialTermSize(80, 24))
			for _, message := range teaMessages(binding.Keys[0]) {
				testModel.Send(message)
			}
			if binding.ID == "quit" || binding.ID == "cancel" {
				testModel.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
			} else if err := testModel.Quit(); err != nil {
				t.Fatal(err)
			}
			program := testModel.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(ProgramModel)
			after := program.Domain
			switch binding.ID {
			case "quit", "cancel":
				if !after.Quit {
					t.Fatal("PTY quit binding did not request quit")
				}
			case "help":
				if !after.HasOverlay(OverlayHelp) {
					t.Fatal("PTY help binding did not open help")
				}
			case "palette":
				if !after.HasOverlay(OverlayPalette) {
					t.Fatal("PTY palette binding did not open palette")
				}
			case "query":
				if !after.QueryActive {
					t.Fatal("PTY query binding did not focus query")
				}
			case "escape":
				if after.Query != "" {
					t.Fatalf("PTY escape left query %q", after.Query)
				}
			case "next-focus", "previous-focus":
				if after.FocusIndex == before.FocusIndex {
					t.Fatal("PTY focus binding did not move focus")
				}
			case "move", "page":
				if after.Focus.ItemID == before.Focus.ItemID {
					t.Fatal("PTY list binding did not move the semantic item")
				}
			case "inspect", "toggle", "section":
				if after.Feedback == "" || after.Feedback == before.Feedback {
					t.Fatalf("PTY binding did not produce intended feedback: %q", after.Feedback)
				}
			case "refresh":
				if !after.RefreshInFlight {
					t.Fatal("PTY refresh binding did not start refresh")
				}
			case "poll":
				if after.Polling == before.Polling {
					t.Fatal("PTY polling binding did not toggle")
				}
			case "go":
				if after.Route != RouteOverview {
					t.Fatalf("PTY go binding route = %s", after.Route)
				}
			default:
				t.Fatalf("registry binding %q lacks a PTY behavior assertion", binding.ID)
			}
		})
	}
}

func teaMessages(keyName string) []tea.Msg {
	if strings.Contains(keyName, " ") {
		parts := strings.Fields(keyName)
		messages := make([]tea.Msg, 0, len(parts))
		for _, part := range parts {
			messages = append(messages, teaMessages(part)...)
		}
		return messages
	}
	types := map[string]tea.KeyType{
		"tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab, "up": tea.KeyUp, "down": tea.KeyDown,
		"left": tea.KeyLeft, "right": tea.KeyRight, "enter": tea.KeyEnter, "space": tea.KeySpace,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
		"esc": tea.KeyEsc, "ctrl+c": tea.KeyCtrlC,
	}
	if keyType, ok := types[keyName]; ok {
		return []tea.Msg{tea.KeyMsg{Type: keyType}}
	}
	return []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keyName)}}
}
