package github

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitPushTreeRefusesUnknownMetadata(t *testing.T) {
	for _, mode := range []string{"empty complete tree", "missing tree", "missing truncation", "nil entry", "missing hash", "missing mode", "missing type", "complete entry"} {
		t.Run(mode, func(t *testing.T) {
			tree := &github.Tree{Truncated: new(false), Entries: []*github.TreeEntry{}}
			entry := &github.TreeEntry{Path: new("main.tf"), SHA: new(strings.Repeat("a", 40)), Mode: new("100644"), Type: new("blob")}
			switch mode {
			case "missing tree":
				tree.Entries = nil
			case "missing truncation":
				tree.Truncated = nil
			case "nil entry":
				tree.Entries = append(tree.Entries, nil)
			case "missing hash":
				entry.SHA = nil
			case "missing mode":
				entry.Mode = nil
			case "missing type":
				entry.Type = nil
			}
			if strings.HasPrefix(mode, "missing ") && mode != "missing tree" && mode != "missing truncation" || mode == "complete entry" {
				tree.Entries = append(tree.Entries, entry)
			}
			err := validatePushTree(tree)
			if mode == "empty complete tree" || mode == "complete entry" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, kernel.ErrUnavailable) {
				t.Fatal("unknown native tree became complete", err)
			}
		})
	}
	if err := validatePushTree(nil); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatal(err)
	}
}
