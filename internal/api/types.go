package api

// Document represents a Google Docs document.
type Document struct {
	DocumentID    string         `json:"documentId"`
	Title         string         `json:"title"`
	RevisionID    string         `json:"revisionId"`
	Body          *Body          `json:"body"`
	DocumentStyle *DocumentStyle `json:"documentStyle"`
}

// Body contains the main document content.
type Body struct {
	Content []*StructuralElement `json:"content"`
}

// StructuralElement represents a piece of content in the document body.
type StructuralElement struct {
	StartIndex   int        `json:"startIndex"`
	EndIndex     int        `json:"endIndex"`
	Paragraph    *Paragraph `json:"paragraph"`
	SectionBreak *struct{}  `json:"sectionBreak"`
}

// Paragraph represents a paragraph element.
type Paragraph struct {
	Elements []*ParagraphElement `json:"elements"`
}

// ParagraphElement represents an element within a paragraph.
type ParagraphElement struct {
	StartIndex int      `json:"startIndex"`
	EndIndex   int      `json:"endIndex"`
	TextRun    *TextRun `json:"textRun"`
}

// TextRun represents a contiguous run of text with consistent styling.
type TextRun struct {
	Content string `json:"content"`
}

// DocumentStyle holds document-level formatting.
type DocumentStyle struct {
	DefaultHeaderID string `json:"defaultHeaderId"`
	DefaultFooterID string `json:"defaultFooterId"`
}

// BatchUpdateRequest is the request body for the batchUpdate endpoint.
type BatchUpdateRequest struct {
	Requests []*Request `json:"requests"`
}

// BatchUpdateResponse is the response from the batchUpdate endpoint.
type BatchUpdateResponse struct {
	DocumentID string   `json:"documentId"`
	Replies    []*Reply `json:"replies"`
}

// Reply represents the response to a single request in a batchUpdate.
type Reply struct {
	ReplaceAllText *ReplaceAllTextReply `json:"replaceAllText"`
}

// ReplaceAllTextReply contains the result of a replaceAllText request.
type ReplaceAllTextReply struct {
	OccurrencesChanged int32 `json:"occurrencesChanged"`
}

// Request represents a single update operation in a batchUpdate.
type Request struct {
	InsertText         *InsertTextRequest         `json:"insertText,omitempty"`
	DeleteContentRange *DeleteContentRangeRequest `json:"deleteContentRange,omitempty"`
	ReplaceAllText     *ReplaceAllTextRequest     `json:"replaceAllText,omitempty"`
}

// InsertTextRequest inserts text at a specified location in the document.
type InsertTextRequest struct {
	Text                 string                `json:"text"`
	Location             *Location             `json:"location,omitempty"`
	EndOfSegmentLocation *EndOfSegmentLocation `json:"endOfSegmentLocation,omitempty"`
}

// Location represents a specific position in the document by index.
type Location struct {
	Index int `json:"index"`
}

// EndOfSegmentLocation represents the end of a document segment.
// An empty SegmentID refers to the main document body.
type EndOfSegmentLocation struct {
	SegmentID string `json:"segmentId"`
}

// DeleteContentRangeRequest deletes all content in the specified range.
type DeleteContentRangeRequest struct {
	Range *Range `json:"range"`
}

// Range represents a contiguous range of content in a document.
type Range struct {
	StartIndex int `json:"startIndex"`
	EndIndex   int `json:"endIndex"`
}

// ReplaceAllTextRequest replaces all instances of the specified text.
type ReplaceAllTextRequest struct {
	ContainsText *SubstringMatchCriteria `json:"containsText"`
	ReplaceText  string                  `json:"replaceText"`
}

// SubstringMatchCriteria defines how to match text for replacement.
type SubstringMatchCriteria struct {
	Text      string `json:"text"`
	MatchCase bool   `json:"matchCase"`
}

// DocsError is returned when the Google Docs API responds with an error.
type DocsError struct {
	StatusCode int
	Message    string
}

func (e *DocsError) Error() string {
	return e.Message
}
