package content_test

import (
	"os"
	"testing"

	"github.com/generalpy101/pygoserver/internal/content"
)

func TestDiv(t *testing.T) {
	templateStr := "<div>Hello World</div>"
	parsedTemplate, err := content.Parse(templateStr)

	if err != nil {
		t.Errorf("There should be no error but there is with: %s", err.Error())
	}

	parsedTemplate.Print()
}

func TestFullHTML(t *testing.T) {
	templateStr := `
		<!DOCTYPE html>
		<html>
			<body>
				<h1> Hello World </h1>
			</body>
		</html>
	`
	parsedTemplate, err := content.Parse(templateStr)

	if err != nil {
		t.Errorf("There should be no error but there is with: %s", err.Error())
	}

	parsedTemplate.Print()
}

func TestUnclosedTag(t *testing.T) {
	templateStr := "<div>Hello World"
	_, err := content.Parse(templateStr)

	t.Log(err.Error())

	if err == nil {
		t.Error("There should be no error but there is nothing")
	}
}

func TestIncompleteTag(t *testing.T) {
	templateStr := "<div Hello World </div>"
	_, err := content.Parse(templateStr)

	t.Log(err.Error())

	if err == nil {
		t.Error("There should be no error but there is nothing")
	}
}

func TestVariableParsing(t *testing.T) {
	templateStr := `
		<!DOCTYPE html>
		<html>
			<body>
				<h1> Hello World {{name}}</h1>
			</body>
		</html>
	`
	parsedTemplate, err := content.Parse(templateStr)

	if err != nil {
		t.Errorf("There should be no error but there is with: %s", err.Error())
	}

	parsedTemplate.Print()
}

func TestRenderingWithVariable(t *testing.T) {
	templateStr := `
		<!DOCTYPE html>
		<html>
			<body>
				<h1> Hello World {{name}}</h1>
			</body>
		</html>
	`

	ctx := content.NewContext()

	ctx.Values["name"] = "Prakash"
	ctx.Values["age"] = 24

	parsedTemplate, err := content.Parse(templateStr)

	if err != nil {
		t.Errorf("There should be no error but there is with: %s", err.Error())
	}

	parsedTemplate.Print()

	rendered, err := parsedTemplate.Render(ctx)

	if err != nil {
		t.Errorf("There should be no error but there is %s", err.Error())
	}

	t.Log(rendered)
}

// func TestEngine(t *testing.T) {
// 	ctx := content.NewContext()

// 	ctx.Values["name"] = "Prakash"
// 	ctx.Values["age"] = 24

// }

func TestMemberAccess(t *testing.T) {
	templateStr := `<p>{{ user.name }} is {{ user.age }} years old</p>`

	ctx := content.NewContext()
	ctx.Values["user"] = map[string]any{
		"name": "Prakash",
		"age":  24,
	}

	parsedTemplate, err := content.Parse(templateStr)

	if err != nil {
		t.Errorf("There should be no error but there is with: %s", err.Error())
	}

	rendered, err := parsedTemplate.Render(ctx)

	if err != nil {
		t.Errorf("There should be no error but there is %s", err.Error())
	}

	t.Log(rendered)

	if rendered != "<p>Prakash is 24 years old</p>" {
		t.Errorf("Unexpected render output: %s", rendered)
	}
}

func TestIfElse(t *testing.T) {
	templateStr := `{{ if show }}<p>visible</p>{{ else }}<p>hidden</p>{{ end }}`

	parsedTemplate, err := content.Parse(templateStr)

	if err != nil {
		t.Errorf("There should be no error but there is with: %s", err.Error())
	}

	ctx := content.NewContext()
	ctx.Values["show"] = true

	rendered, err := parsedTemplate.Render(ctx)

	if err != nil {
		t.Errorf("There should be no error but there is %s", err.Error())
	}

	t.Log(rendered)

	if rendered != "<p>visible</p>" {
		t.Errorf("Expected the if branch, got: %s", rendered)
	}

	ctx.Values["show"] = false

	rendered, err = parsedTemplate.Render(ctx)

	if err != nil {
		t.Errorf("There should be no error but there is %s", err.Error())
	}

	t.Log(rendered)

	if rendered != "<p>hidden</p>" {
		t.Errorf("Expected the else branch, got: %s", rendered)
	}
}

func TestForLoop(t *testing.T) {
	templateStr := `<ul>{{ for todo in todos }}<li>{{ todo.title }}</li>{{ end }}</ul>`

	ctx := content.NewContext()
	ctx.Values["todos"] = []any{
		map[string]any{"title": "buy milk"},
		map[string]any{"title": "write code"},
	}

	parsedTemplate, err := content.Parse(templateStr)

	if err != nil {
		t.Errorf("There should be no error but there is with: %s", err.Error())
	}

	parsedTemplate.Print()

	rendered, err := parsedTemplate.Render(ctx)

	if err != nil {
		t.Errorf("There should be no error but there is %s", err.Error())
	}

	t.Log(rendered)

	expected := "<ul><li>buy milk</li><li>write code</li></ul>"

	if rendered != expected {
		t.Errorf("Expected %q, got %q", expected, rendered)
	}
}

func TestTemplateInheritance(t *testing.T) {
	// A base template with a default block, and a child that overrides it.
	dir := t.TempDir()

	base := `<html><body>{{ block "content" }}default content{{ end }}</body></html>`
	child := `{{ extends "base.gotemp" }}{{ block "content" }}child content{{ end }}`

	if err := os.WriteFile(dir+"/base.gotemp", []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(dir+"/child.gotemp", []byte(child), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, err := content.NewEngine(dir)

	if err != nil {
		t.Fatalf("There should be no error but there is with: %s", err.Error())
	}

	// Rendering the base alone gives the default block content.
	rendered, err := engine.GetTemplate("base.gotemp", content.NewContext())

	if err != nil {
		t.Errorf("There should be no error but there is %s", err.Error())
	}

	t.Log(rendered)

	if rendered != "<html><body>default content</body></html>" {
		t.Errorf("Unexpected base render: %s", rendered)
	}

	// Rendering the child renders the base with the overridden block.
	rendered, err = engine.GetTemplate("child.gotemp", content.NewContext())

	if err != nil {
		t.Errorf("There should be no error but there is %s", err.Error())
	}

	t.Log(rendered)

	if rendered != "<html><body>child content</body></html>" {
		t.Errorf("Unexpected child render: %s", rendered)
	}
}
