package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// Assert registration without starting unrelated DB/notification jobs or
// waiting a real minute. Queue expiry behavior is tested in web/agent.
func TestEventQueueSweepUsesExistingMinuteScheduler(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "runtime.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	registrations := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "registerScheduledWork" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 3 {
				return true
			}
			name, ok := call.Args[0].(*ast.BasicLit)
			if !ok || name.Value != strconv.Quote("agent:sweepEventQueues") {
				return true
			}
			registrations++
			callee, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || callee.Sel.Name != "AddFunc" {
				t.Fatal("queue sweep no longer uses scheduler.AddFunc")
			}
			owner, ok := callee.X.(*ast.Ident)
			if !ok || owner.Name != "scheduler" {
				t.Fatal("queue sweep no longer uses existing scheduler")
			}
			spec, ok := call.Args[1].(*ast.BasicLit)
			if !ok || spec.Value != strconv.Quote("@every 1m") {
				t.Fatal("queue sweep interval changed")
			}
			callback, ok := call.Args[2].(*ast.SelectorExpr)
			if !ok || callback.Sel.Name != "SweepV2EventQueues" {
				t.Fatal("scheduler no longer invokes queue cleanup")
			}
			return true
		})
	}
	if registrations != 1 {
		t.Fatalf("queue sweep registrations = %d, want 1", registrations)
	}
}
