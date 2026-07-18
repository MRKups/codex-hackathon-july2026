package domain

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
)

// ValidateSignature accepts one bodyless, top-level Go function signature that can be used by
// both an independently generated test and a candidate implementation. It checks syntax and
// type validity before a browser run can spend a provider request.
func ValidateSignature(signature string) error {
	source := "package solution\n\n" + strings.TrimSpace(signature) + " {\n\tpanic(\"signature validation stub\")\n}\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "signature.go", source, 0)
	if err != nil {
		return fmt.Errorf("parse signature: %w", err)
	}
	if len(file.Decls) != 1 {
		return errors.New("signature must declare exactly one function")
	}

	function, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || function.Name == nil || function.Recv != nil {
		return errors.New("signature must declare one top-level function")
	}
	if _, err := new(types.Config).Check("solution", fset, []*ast.File{file}, nil); err != nil {
		return fmt.Errorf("type-check signature: %w", err)
	}
	return nil
}
