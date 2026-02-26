package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/the20100/g-docs-cli/internal/api"
	"github.com/the20100/g-docs-cli/internal/output"
)

var docCmd = &cobra.Command{
	Use:   "doc",
	Short: "Manage Google Docs documents",
}

// ---- doc create ----

var docCreateCmd = &cobra.Command{
	Use:   "create <title>",
	Short: "Create a new blank Google Doc",
	Long: `Create a new blank Google Doc with the given title.

Returns the document ID and URL.

Examples:
  gdocs doc create "My New Document"
  gdocs doc create "Meeting Notes" --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		doc, err := client.CreateDocument(args[0])
		if err != nil {
			return err
		}
		if output.IsJSON(cmd) {
			return output.PrintJSON(doc, output.IsPretty(cmd))
		}
		fmt.Printf("Document created: %s\n", doc.Title)
		fmt.Printf("ID:  %s\n", doc.DocumentID)
		fmt.Printf("URL: https://docs.google.com/document/d/%s/edit\n", doc.DocumentID)
		return nil
	},
}

// ---- doc get ----

var docGetCmd = &cobra.Command{
	Use:   "get <document-id>",
	Short: "Get document metadata",
	Long: `Get metadata for a Google Doc by its ID.

Shows title, ID, and revision. Use 'doc content' to read the text.

Examples:
  gdocs doc get 1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms
  gdocs doc get <id> --pretty`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		doc, err := client.GetDocument(args[0])
		if err != nil {
			return err
		}
		if output.IsJSON(cmd) {
			return output.PrintJSON(doc, output.IsPretty(cmd))
		}
		output.PrintKeyValue([][]string{
			{"ID", doc.DocumentID},
			{"Title", doc.Title},
			{"Revision", doc.RevisionID},
			{"URL", "https://docs.google.com/document/d/" + doc.DocumentID + "/edit"},
		})
		return nil
	},
}

// ---- doc content ----

var docContentCmd = &cobra.Command{
	Use:   "content <document-id>",
	Short: "Read the plain text content of a document",
	Long: `Extract and print the plain text content of a Google Doc.

Traverses the document body and concatenates all text runs.

Examples:
  gdocs doc content 1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms
  gdocs doc content <id> | grep "keyword"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		doc, err := client.GetDocument(args[0])
		if err != nil {
			return err
		}
		if output.IsJSON(cmd) {
			type contentResult struct {
				DocumentID string `json:"documentId"`
				Title      string `json:"title"`
				Text       string `json:"text"`
			}
			return output.PrintJSON(contentResult{
				DocumentID: doc.DocumentID,
				Title:      doc.Title,
				Text:       api.ExtractText(doc),
			}, output.IsPretty(cmd))
		}
		text := api.ExtractText(doc)
		if text == "" {
			fmt.Println("(document is empty)")
			return nil
		}
		fmt.Print(text)
		return nil
	},
}

// ---- doc insert ----

var (
	docInsertIndex int
	docInsertAtEnd bool
)

var docInsertCmd = &cobra.Command{
	Use:   "insert <document-id> <text>",
	Short: "Insert text into a document",
	Long: `Insert text into a Google Doc at a specific index or at the end.

Index 1 is the very start of the document body. Indices increase with each
character, including newlines. Use 'gdocs doc get <id> --pretty' to inspect
the document structure.

By default (no --index flag), text is appended at the end of the document.

Examples:
  gdocs doc insert <id> "Hello, world!"
  gdocs doc insert <id> "Title text" --index 1
  gdocs doc insert <id> $'Line one\nLine two\n'`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		documentID := args[0]
		text := args[1]

		var req *api.BatchUpdateRequest
		if cmd.Flags().Changed("index") {
			req = &api.BatchUpdateRequest{
				Requests: []*api.Request{
					{
						InsertText: &api.InsertTextRequest{
							Text:     text,
							Location: &api.Location{Index: docInsertIndex},
						},
					},
				},
			}
		} else {
			// Insert at end of document body (segmentId "" = main body)
			req = &api.BatchUpdateRequest{
				Requests: []*api.Request{
					{
						InsertText: &api.InsertTextRequest{
							Text:                 text,
							EndOfSegmentLocation: &api.EndOfSegmentLocation{SegmentID: ""},
						},
					},
				},
			}
		}

		_, err := client.BatchUpdate(documentID, req)
		if err != nil {
			return err
		}

		if output.IsJSON(cmd) {
			return output.PrintJSON(map[string]any{
				"documentId": documentID,
				"inserted":   text,
			}, output.IsPretty(cmd))
		}

		preview := text
		if len([]rune(preview)) > 60 {
			preview = string([]rune(preview)[:59]) + "…"
		}
		preview = strings.ReplaceAll(preview, "\n", "\\n")
		fmt.Printf("Text inserted: %q\n", preview)
		fmt.Printf("Document URL: https://docs.google.com/document/d/%s/edit\n", documentID)
		return nil
	},
}

// ---- doc replace ----

var (
	docReplaceFind      string
	docReplaceWith      string
	docReplaceCaseSensitive bool
)

var docReplaceCmd = &cobra.Command{
	Use:   "replace <document-id>",
	Short: "Find and replace text in a document",
	Long: `Replace all occurrences of a string in a Google Doc.

By default the search is case-insensitive. Use --case-sensitive to match case.

Examples:
  gdocs doc replace <id> --find "Hello" --replace "Hi"
  gdocs doc replace <id> --find "TODO" --replace "DONE" --case-sensitive`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if docReplaceFind == "" {
			return fmt.Errorf("--find is required")
		}

		req := &api.BatchUpdateRequest{
			Requests: []*api.Request{
				{
					ReplaceAllText: &api.ReplaceAllTextRequest{
						ContainsText: &api.SubstringMatchCriteria{
							Text:      docReplaceFind,
							MatchCase: docReplaceCaseSensitive,
						},
						ReplaceText: docReplaceWith,
					},
				},
			},
		}

		resp, err := client.BatchUpdate(args[0], req)
		if err != nil {
			return err
		}

		if output.IsJSON(cmd) {
			return output.PrintJSON(resp, output.IsPretty(cmd))
		}

		var occurrences int32
		if len(resp.Replies) > 0 && resp.Replies[0].ReplaceAllText != nil {
			occurrences = resp.Replies[0].ReplaceAllText.OccurrencesChanged
		}
		fmt.Printf("Replaced %d occurrence(s) of %q with %q\n", occurrences, docReplaceFind, docReplaceWith)
		return nil
	},
}

// ---- doc delete-range ----

var (
	docDeleteStart int
	docDeleteEnd   int
)

var docDeleteRangeCmd = &cobra.Command{
	Use:   "delete-range <document-id>",
	Short: "Delete a range of content from a document",
	Long: `Delete content between startIndex (inclusive) and endIndex (exclusive).

Indices correspond to UTF-16 code units in the document. Use 'gdocs doc get <id> --pretty'
to inspect StructuralElement start/end indices.

Examples:
  gdocs doc delete-range <id> --start 1 --end 10
  gdocs doc delete-range <id> --start 5 --end 20`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("start") {
			return fmt.Errorf("--start is required")
		}
		if !cmd.Flags().Changed("end") {
			return fmt.Errorf("--end is required")
		}
		if docDeleteStart >= docDeleteEnd {
			return fmt.Errorf("--start (%d) must be less than --end (%d)", docDeleteStart, docDeleteEnd)
		}

		req := &api.BatchUpdateRequest{
			Requests: []*api.Request{
				{
					DeleteContentRange: &api.DeleteContentRangeRequest{
						Range: &api.Range{
							StartIndex: docDeleteStart,
							EndIndex:   docDeleteEnd,
						},
					},
				},
			},
		}

		_, err := client.BatchUpdate(args[0], req)
		if err != nil {
			return err
		}

		if output.IsJSON(cmd) {
			return output.PrintJSON(map[string]any{
				"documentId": args[0],
				"deleted":    map[string]int{"startIndex": docDeleteStart, "endIndex": docDeleteEnd},
			}, output.IsPretty(cmd))
		}

		fmt.Printf("Deleted content from index %d to %d\n", docDeleteStart, docDeleteEnd)
		fmt.Printf("Document URL: https://docs.google.com/document/d/%s/edit\n", args[0])
		return nil
	},
}

func init() {
	// insert flags
	docInsertCmd.Flags().IntVar(&docInsertIndex, "index", 0, "Character index at which to insert (default: end of document)")

	// replace flags
	docReplaceCmd.Flags().StringVar(&docReplaceFind, "find", "", "Text to find (required)")
	docReplaceCmd.Flags().StringVar(&docReplaceWith, "replace", "", "Replacement text")
	docReplaceCmd.Flags().BoolVar(&docReplaceCaseSensitive, "case-sensitive", false, "Match case when searching")

	// delete-range flags
	docDeleteRangeCmd.Flags().IntVar(&docDeleteStart, "start", 0, "Start index of range to delete (inclusive, required)")
	docDeleteRangeCmd.Flags().IntVar(&docDeleteEnd, "end", 0, "End index of range to delete (exclusive, required)")

	docCmd.AddCommand(
		docCreateCmd,
		docGetCmd,
		docContentCmd,
		docInsertCmd,
		docReplaceCmd,
		docDeleteRangeCmd,
	)
	rootCmd.AddCommand(docCmd)
}
