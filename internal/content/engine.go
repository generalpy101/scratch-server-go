package content

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

//
// How a template engine works (in 3 steps):
//
// 1. PARSE: read the template file once and turn it into a tree of nodes
//    (the "AST", abstract syntax tree). HTML tags become ElementNodes,
//    plain text becomes TextNodes, and {{ ... }} directives become
//    VariableNode / IfNode / ForNode / BlockNode.
//
// 2. CACHE: parsing is expensive (lots of string scanning), so the Engine
//    keeps the parsed tree in memory and reuses it for every request.
//
// 3. RENDER: walk the tree top to bottom. TextNodes are copied as-is,
//    and every directive node looks up values in a Context (a bag of
//    variables) and decides what to output.
//
// The big idea: parsing happens ONCE per template, rendering happens
// ONCE PER REQUEST with fresh data.
//

//
// AST
//

type Node interface {
	node()
}

type Template struct {
	Root *ElementNode

	// Name of the base template this one extends ("" = no inheritance).
	Extends string

	// All {{ block "name" }} nodes found in this template, by name.
	// Used to override blocks of the base template.
	Blocks map[string]*BlockNode
}

type ElementNode struct {
	Tag      string
	Children []Node
}

func (*ElementNode) node() {}

type TextNode struct {
	Value string
}

func (TextNode) node() {}

type VariableNode struct {
	// Name can be a plain name ("title") or a dotted path ("todo.title").
	Name string
}

func (VariableNode) node() {}

// {{ if condition }} Then {{ else }} Else {{ end }}
type IfNode struct {
	Cond string
	Then []Node
	Else []Node

	// Parser bookkeeping: false while we fill Then, true after {{ else }}.
	inElse bool
}

func (*IfNode) node() {}

// {{ for item in items }} Body {{ end }}
type ForNode struct {
	Var  string // loop variable name ("item")
	List string // list variable name ("items")
	Body []Node
}

func (*ForNode) node() {}

// {{ block "name" }} default content {{ end }}
//
// Blocks are the extension points for template inheritance: a child
// template can replace the content of a block of its base template.
type BlockNode struct {
	Name     string
	Children []Node
}

func (*BlockNode) node() {}

//
// While parsing we keep a stack of the nodes we are currently inside.
// Anything we parse is appended to the node on top of the stack.
// ElementNode, IfNode, ForNode and BlockNode can all hold children,
// so they share this small interface.
//

type containerNode interface {
	Node
	appendChild(child Node)
}

func (n *ElementNode) appendChild(child Node) {
	n.Children = append(n.Children, child)
}

func (n *IfNode) appendChild(child Node) {
	if n.inElse {
		n.Else = append(n.Else, child)
	} else {
		n.Then = append(n.Then, child)
	}
}

func (n *ForNode) appendChild(child Node) {
	n.Body = append(n.Body, child)
}

func (n *BlockNode) appendChild(child Node) {
	n.Children = append(n.Children, child)
}

//
// Context
//
// A Context is the bag of variables a template can use. Contexts form a
// chain: a {{ for }} loop creates a child context holding the loop
// variable, which can still "see" the parent's variables. This is called
// lexical scoping and it is how most interpreters resolve variables too.
//

type Context struct {
	Values map[string]any
	parent *Context
}

func NewContext() *Context {
	return &Context{
		Values: make(map[string]any),
	}
}

// newChild creates a scope nested inside c. Variables set on the child
// shadow the parent's variables with the same name.
func (c *Context) newChild() *Context {
	return &Context{
		Values: make(map[string]any),
		parent: c,
	}
}

// lookup walks the scope chain (child -> parent -> ...) looking for name.
func (c *Context) lookup(name string) (any, bool) {
	for scope := c; scope != nil; scope = scope.parent {
		if value, found := scope.Values[name]; found {
			return value, true
		}
	}
	return nil, false
}

// Lookup resolves a variable name like "title" or "todo.title".
//
// For a dotted path we first resolve the first part ("todo") through the
// scope chain, then walk the remaining parts ("title") through nested
// maps. Real engines (text/template) use reflection here so struct
// fields also work; we keep it simple and support maps only.
func (c *Context) Lookup(name string) (any, error) {
	parts := strings.Split(name, ".")

	value, found := c.lookup(parts[0])
	if !found {
		return nil, fmt.Errorf("context variable %q not found", name)
	}

	for _, part := range parts[1:] {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot access %q of %q: not an object", part, name)
		}
		value, found = object[part]
		if !found {
			return nil, fmt.Errorf("context variable %q not found", name)
		}
	}

	return value, nil
}

//
// Parser
//
// The parser walks the input once, left to right. At every position it
// looks at what comes next:
//
//	"{{" -> a directive (variable, if, for, block, ...)
//	"<"  -> an HTML tag
//	else -> plain text up to the next "<" or "{{"
//

func Parse(input string) (*Template, error) {
	root := &ElementNode{
		Tag: "root",
	}

	template := &Template{
		Root:   root,
		Blocks: make(map[string]*BlockNode),
	}

	// The stack contains the nodes we're currently inside.
	//
	// For:
	//
	// <ul>
	//     {{ for todo in todos }}
	//     <li>{{ todo.title }}</li>
	//     {{ end }}
	// </ul>
	//
	// while reading "{{ todo.title }}", stack is:
	//
	// [root, ul, for, li]
	stack := []containerNode{root}

	i := 0

	for i < len(input) {
		top := stack[len(stack)-1]

		//
		// Directive: {{ ... }}
		//
		if strings.HasPrefix(input[i:], "{{") {
			end := strings.Index(input[i:], "}}")

			if end == -1 {
				return nil, fmt.Errorf("unclosed {{ starting at position %d", i)
			}

			directive := strings.TrimSpace(input[i+2 : i+end])

			if directive == "" {
				return nil, fmt.Errorf("empty directive at position %d", i)
			}

			if err := handleDirective(template, directive, &stack); err != nil {
				return nil, err
			}

			i += end + 2
			continue
		}

		//
		// We found the beginning of a tag.
		//
		if input[i] == '<' {

			if strings.HasPrefix(strings.ToUpper(input[i:]), "<!DOCTYPE") {
				end := strings.IndexByte(input[i:], '>')

				if end == -1 {
					return nil, fmt.Errorf("unclosed doctype declaration")
				}

				i += end + 1
				continue
			}

			end := strings.IndexByte(input[i:], '>')

			if end == -1 {
				return nil, fmt.Errorf(
					"unclosed tag starting at position %d",
					i,
				)
			}

			// Convert the relative position returned by
			// IndexByte into an absolute position.
			end += i

			tagContent := input[i+1 : end]

			//
			// Closing tag:
			//
			// </div>
			//
			if strings.HasPrefix(tagContent, "/") {
				tagName := strings.TrimSpace(
					tagContent[1:],
				)

				if tagName == "" {
					return nil, fmt.Errorf(
						"empty closing tag at position %d",
						i,
					)
				}

				// We cannot close root.
				if len(stack) == 1 {
					return nil, fmt.Errorf(
						"unexpected closing tag </%s>",
						tagName,
					)
				}

				// The top of the stack must be the element we
				// are closing. If it is an if/for/block, the
				// template forgot its {{ end }} first.
				current, ok := top.(*ElementNode)
				if !ok {
					return nil, fmt.Errorf(
						"expected {{ end }}, got </%s>",
						tagName,
					)
				}

				//
				// Make sure:
				//
				// <div>
				// </span>
				//
				// is rejected.
				//
				if !strings.HasPrefix(current.Tag, tagName) {
					return nil, fmt.Errorf(
						"expected </%s>, got </%s>",
						current.Tag,
						tagName,
					)
				}

				// We're no longer inside this element.
				stack = stack[:len(stack)-1]

				i = end + 1
				continue
			}

			//
			// Opening tag:
			//
			// <div>
			//
			tagName := strings.TrimSpace(tagContent)

			if tagName == "" {
				return nil, fmt.Errorf(
					"empty tag at position %d",
					i,
				)
			}

			element := &ElementNode{
				Tag: tagName,
			}

			//
			// Add the new element to the
			// current node's children.
			//
			top.appendChild(element)

			//
			// We are now inside this element.
			//
			stack = append(stack, element)

			i = end + 1
			continue
		}

		//
		// If we reach here, we're reading text.
		// Read up to the next '<' (tag) or "{{" (directive),
		// whichever comes first.
		//
		next := len(input)

		if nextTag := strings.IndexByte(input[i:], '<'); nextTag != -1 {
			next = min(next, i+nextTag)
		}

		if nextDirective := strings.Index(input[i:], "{{"); nextDirective != -1 {
			next = min(next, i+nextDirective)
		}

		text := input[i:next]
		i = next

		// Skip whitespace-only text: it is just template indentation.
		if strings.TrimSpace(text) != "" {
			top.appendChild(TextNode{
				Value: text,
			})
		}
	}

	//
	// If stack contains anything other than root,
	// something was left unclosed.
	//
	if len(stack) != 1 {
		return nil, fmt.Errorf(
			"unclosed %s",
			containerName(stack[len(stack)-1]),
		)
	}

	return template, nil
}

// handleDirective parses the inside of a single {{ ... }} and updates the
// template and/or the stack:
//
//	{{ name }}              -> VariableNode, appended to the current node
//	{{ if cond }}           -> push an IfNode on the stack
//	{{ else }}              -> flip the IfNode on top to its Else branch
//	{{ for item in list }}  -> push a ForNode on the stack
//	{{ block "name" }}      -> push a BlockNode, remember it by name
//	{{ end }}               -> pop the innermost if/for/block
//	{{ extends "base" }}    -> mark the template as a child of "base"
func handleDirective(template *Template, directive string, stack *[]containerNode) error {
	top := (*stack)[len(*stack)-1]

	switch {

	case directive == "end":
		// Pops the innermost if/for/block. If the top is an HTML
		// element instead, the template forgot to close a tag.
		if len(*stack) == 1 {
			return fmt.Errorf("unexpected {{ end }}")
		}

		if element, isElement := top.(*ElementNode); isElement {
			return fmt.Errorf("unclosed tag <%s>, expected </> before {{ end }}", element.Tag)
		}

		*stack = (*stack)[:len(*stack)-1]

	case directive == "else":
		// "else" only makes sense directly inside an if.
		ifNode, ok := top.(*IfNode)
		if !ok {
			return fmt.Errorf("unexpected {{ else }}")
		}

		ifNode.inElse = true

	case strings.HasPrefix(directive, "if "):
		condition := strings.TrimSpace(directive[len("if "):])

		node := &IfNode{Cond: condition}
		top.appendChild(node)
		*stack = append(*stack, node)

	case strings.HasPrefix(directive, "for "):
		// Expected shape: "for item in items"
		fields := strings.Fields(directive)

		if len(fields) != 4 || fields[2] != "in" {
			return fmt.Errorf(
				"invalid for syntax %q, want: {{ for item in items }}",
				directive,
			)
		}

		node := &ForNode{
			Var:  fields[1],
			List: fields[3],
		}
		top.appendChild(node)
		*stack = append(*stack, node)

	case strings.HasPrefix(directive, "block "):
		name := strings.Trim(strings.TrimSpace(directive[len("block "):]), `"`)

		if name == "" {
			return fmt.Errorf("block needs a name, want: {{ block \"name\" }}")
		}

		node := &BlockNode{Name: name}
		top.appendChild(node)
		*stack = append(*stack, node)

		// Remember every block by name so a child template
		// can override the blocks of its base template.
		template.Blocks[name] = node

	case strings.HasPrefix(directive, "extends "):
		template.Extends = strings.Trim(strings.TrimSpace(directive[len("extends "):]), `"`)

	default:
		// Anything else is a variable: {{ name }} or {{ todo.title }}
		top.appendChild(VariableNode{
			Name: directive,
		})
	}

	return nil
}

// containerName describes a node for error messages.
func containerName(node containerNode) string {
	switch n := node.(type) {
	case *ElementNode:
		return fmt.Sprintf("tag <%s>", n.Tag)
	case *IfNode:
		return "{{ if }}"
	case *ForNode:
		return "{{ for }}"
	case *BlockNode:
		return fmt.Sprintf("{{ block %q }}", n.Name)
	default:
		return "node"
	}
}

func (t *Template) Print() {
	fmt.Println("Template")
	if t.Extends != "" {
		fmt.Printf("    Extends: %s\n", t.Extends)
	}
	printNode(t.Root, "", "└── ")
}

func printNode(node Node, prefix string, connector string) {
	switch n := node.(type) {

	case *ElementNode:
		fmt.Printf("%s%sElement: %s\n", prefix, connector, n.Tag)

		for i, child := range n.Children {
			childPrefix := prefix + "    "

			childConnector := "├── "
			if i == len(n.Children)-1 {
				childConnector = "└── "
			}

			printNode(child, childPrefix, childConnector)
		}

	case TextNode:
		fmt.Printf(
			"%s%sText: %q\n",
			prefix,
			connector,
			n.Value,
		)

	case VariableNode:
		fmt.Printf(
			"%s%sVariable: %q\n",
			prefix,
			connector,
			n.Name,
		)

	case *IfNode:
		fmt.Printf("%s%sIf: %q\n", prefix, connector, n.Cond)

		for _, child := range n.Then {
			printNode(child, prefix+"    ", "├── ")
		}

		for _, child := range n.Else {
			printNode(child, prefix+"    ", "├── (else) ")
		}

	case *ForNode:
		fmt.Printf("%s%sFor: %q in %q\n", prefix, connector, n.Var, n.List)

		for _, child := range n.Body {
			printNode(child, prefix+"    ", "├── ")
		}

	case *BlockNode:
		fmt.Printf("%s%sBlock: %q\n", prefix, connector, n.Name)

		for _, child := range n.Children {
			printNode(child, prefix+"    ", "├── ")
		}

	default:
		fmt.Printf(
			"%s%sUnknown node: %T\n",
			prefix,
			connector,
			node,
		)
	}
}

//
// Renderer
//
// Rendering walks the AST and builds the output string. The "overrides"
// map is for template inheritance: when a child template extends a base,
// we render the BASE template but hand it the child's blocks, so every
// {{ block }} the base hits can be swapped for the child's version.
//

func (t *Template) Render(ctx *Context) (string, error) {
	return t.renderWithOverrides(ctx, nil)
}

func (t *Template) renderWithOverrides(ctx *Context, overrides map[string]*BlockNode) (string, error) {
	return renderNodes(t.Root.Children, ctx, overrides)
}

func renderNodes(nodes []Node, ctx *Context, overrides map[string]*BlockNode) (string, error) {
	var output strings.Builder

	for _, node := range nodes {
		rendered, err := renderNode(node, ctx, overrides)

		if err != nil {
			return "", err
		}

		output.WriteString(rendered)
	}

	return output.String(), nil
}

func renderNode(node Node, ctx *Context, overrides map[string]*BlockNode) (string, error) {
	switch n := node.(type) {

	case TextNode:
		return n.Value, nil

	case VariableNode:
		value, err := ctx.Lookup(n.Name)

		if err != nil {
			return "", err
		}

		return fmt.Sprint(value), nil

	case *ElementNode:
		var output strings.Builder

		output.WriteString("<")
		output.WriteString(n.Tag)
		output.WriteString(">")

		rendered, err := renderNodes(n.Children, ctx, overrides)

		if err != nil {
			return "", err
		}

		output.WriteString(rendered)

		// n.Tag holds the whole opening tag ("div class=\"x\"), but a
		// closing tag must be just the name ("div").
		tagName, _, _ := strings.Cut(n.Tag, " ")

		output.WriteString("</")
		output.WriteString(tagName)
		output.WriteString(">")

		return output.String(), nil

	case *IfNode:
		value, err := ctx.Lookup(n.Cond)

		if err != nil {
			return "", err
		}

		if isTruthy(value) {
			return renderNodes(n.Then, ctx, overrides)
		}

		return renderNodes(n.Else, ctx, overrides)

	case *ForNode:
		value, err := ctx.Lookup(n.List)

		if err != nil {
			return "", err
		}

		// We only support looping over []any (which is what you get
		// from decoding a JSON array). Real engines use reflection
		// to loop over any slice type.
		items, ok := value.([]any)
		if !ok {
			return "", fmt.Errorf("{{ for }}: %q is not a list", n.List)
		}

		var output strings.Builder

		for _, item := range items {
			// Each iteration gets its own child scope holding the
			// loop variable, so {{ item }} (or {{ todo.title }})
			// resolves to the current element.
			scope := ctx.newChild()
			scope.Values[n.Var] = item

			rendered, err := renderNodes(n.Body, scope, overrides)

			if err != nil {
				return "", err
			}

			output.WriteString(rendered)
		}

		return output.String(), nil

	case *BlockNode:
		//
		// Template inheritance: if the child template defined a block
		// with the same name, render the CHILD's content instead of
		// the default content.
		//
		// The "override != n" check stops infinite recursion: while we
		// are rendering the overriding block itself, n IS the override.
		//
		if override, exists := overrides[n.Name]; exists && override != n {
			return renderNodes(override.Children, ctx, overrides)
		}

		return renderNodes(n.Children, ctx, overrides)

	default:
		return "", fmt.Errorf(
			"unknown node type %T",
			node,
		)
	}
}

//
// isTruthy decides whether an {{ if }} condition counts as true.
// Like Python/JavaScript: false, nil, empty strings, zero and empty
// lists/maps are falsy, everything else is truthy.
//

func isTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case int:
		return v != 0
	case float64:
		return v != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

//
// Engine
//
// The Engine owns the template directory and a cache of parsed templates.
//
// Without a cache, every request would read the file from disk and parse
// it again. With a cache, that work happens once and every request just
// walks the ready-made AST with fresh data.
//
// The cache is guarded by a RWMutex because our server handles requests
// concurrently: many goroutines may read templates at the same time.
//

type Engine struct {
	root  *os.Root
	mu    sync.RWMutex
	cache map[string]*Template
}

func NewEngine(rootDir string) (*Engine, error) {
	root, err := os.OpenRoot(rootDir)

	if err != nil {
		return nil, fmt.Errorf("Error during opening root for templates: %s", err.Error())
	}

	engine := Engine{
		root:  root,
		cache: make(map[string]*Template),
	}

	return &engine, nil
}

// load returns the parsed template, parsing and caching it on first use.
//
// Note: cached templates never expire, so template changes on disk need
// a server restart. (Real engines often check the file's modification
// time and re-parse when it changes.)
func (e *Engine) load(templateName string) (*Template, error) {
	// Fast path: already parsed. Many goroutines can read at once.
	e.mu.RLock()
	cached, found := e.cache[templateName]
	e.mu.RUnlock()

	if found {
		return cached, nil
	}

	// Slow path: read + parse + cache.
	templateContent, err := e.root.ReadFile(templateName)

	if err != nil {
		return nil, fmt.Errorf("Error while opening template: %s", err.Error())
	}

	parsed, err := Parse(string(templateContent))

	if err != nil {
		return nil, fmt.Errorf("Error while parsing template: %s", err.Error())
	}

	e.mu.Lock()
	e.cache[templateName] = parsed
	e.mu.Unlock()

	return parsed, nil
}

func (e *Engine) GetTemplate(templateName string, ctx *Context) (string, error) {
	/* Return a parsed template from context */
	template, err := e.load(templateName)

	if err != nil {
		log.Print(err)
		return "", err
	}

	//
	// Template inheritance: if this template extends a base, we render
	// the BASE template and hand it this template's blocks as overrides.
	// Every {{ block "name" }} in the base then shows the child's content
	// (or its own default content if the child didn't override it).
	//
	if template.Extends != "" {
		base, err := e.load(template.Extends)

		if err != nil {
			log.Print(err)
			return "", err
		}

		content, err := base.renderWithOverrides(ctx, template.Blocks)

		if err != nil {
			err = fmt.Errorf("Error while rendering template: %s", err.Error())
			log.Print(err)
			return "", err
		}

		return content, nil
	}

	content, err := template.renderWithOverrides(ctx, nil)

	if err != nil {
		err = fmt.Errorf("Error while rendering template: %s", err.Error())
		log.Print(err)
		return "", err
	}

	return content, nil
}
