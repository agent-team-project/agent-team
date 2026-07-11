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
		for _, key := range binding.Keys {
			key := key
			t.Run(binding.ID+"/"+strings.ReplaceAll(key, " ", "+"), func(t *testing.T) {
				domain := bindingTestModel(binding.ID, key)
				domain.Capabilities.Dumb = true
				before := domain
				testModel := teatest.NewTestModel(t, NewTestProgramModel(domain), teatest.WithInitialTermSize(80, 24))
				for _, message := range teaMessages(key) {
					testModel.Send(message)
				}
				if binding.ID == "quit" || binding.ID == "cancel" {
					testModel.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
				} else if err := testModel.Quit(); err != nil {
					t.Fatal(err)
				}
				program := testModel.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(ProgramModel)
				assertBindingEffect(t, binding.ID, key, before, program.Domain, nil, false)
			})
		}
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
