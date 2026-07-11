package tui

type Binding struct {
	ID          string
	Keys        []string
	Label       string
	Description string
	Global      bool
}

var bindings = []Binding{
	{ID: "quit", Keys: []string{"q"}, Label: "q", Description: "quit", Global: true},
	{ID: "cancel", Keys: []string{"ctrl+c"}, Label: "Ctrl+C", Description: "cancel or quit", Global: true},
	{ID: "help", Keys: []string{"?"}, Label: "?", Description: "help", Global: true},
	{ID: "palette", Keys: []string{"ctrl+k"}, Label: "Ctrl+K", Description: "commands", Global: true},
	{ID: "query", Keys: []string{"/"}, Label: "/", Description: "filter", Global: true},
	{ID: "escape", Keys: []string{"esc"}, Label: "Esc", Description: "clear or back", Global: true},
	{ID: "next-focus", Keys: []string{"tab"}, Label: "Tab", Description: "next focus", Global: true},
	{ID: "previous-focus", Keys: []string{"shift+tab"}, Label: "Shift+Tab", Description: "previous focus", Global: true},
	{ID: "move", Keys: []string{"up", "down", "left", "right", "h", "j", "k", "l"}, Label: "arrows/hjkl", Description: "move", Global: true},
	{ID: "inspect", Keys: []string{"enter"}, Label: "Enter", Description: "inspect", Global: true},
	{ID: "toggle", Keys: []string{"space"}, Label: "Space", Description: "toggle", Global: true},
	{ID: "page", Keys: []string{"pgup", "pgdown", "home", "end"}, Label: "PgUp/PgDn", Description: "page", Global: true},
	{ID: "section", Keys: []string{"[", "]"}, Label: "[/]", Description: "section", Global: true},
	{ID: "refresh", Keys: []string{"r"}, Label: "r", Description: "refresh", Global: true},
	{ID: "poll", Keys: []string{"p"}, Label: "p", Description: "pause polling", Global: true},
	{ID: "go", Keys: []string{"g o", "g w", "g f", "g a", "g l", "g s", "g r", "g e"}, Label: "g+key", Description: "screen", Global: true},
}

func Bindings() []Binding {
	out := make([]Binding, len(bindings))
	copy(out, bindings)
	return out
}
