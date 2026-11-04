// Package notes is the worked example feature. Copy its shape for new
// features: typed I/O structs (schema + validation), a Service that holds
// business rules, sqlc queries for storage, and a job for side effects.
package notes

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ShammiAnand/go-service-template/internal/apperr"
	"github.com/ShammiAnand/go-service-template/internal/auth"
	"github.com/ShammiAnand/go-service-template/internal/db/dbgen"
	"github.com/ShammiAnand/go-service-template/internal/email"
	"github.com/ShammiAnand/go-service-template/internal/jobs"
)

// Note is the API representation of a note.
type Note struct {
	ID        uuid.UUID `json:"id" format:"uuid"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func fromRow(n dbgen.Note) Note {
	return Note{ID: n.ID, Title: n.Title, Body: n.Body, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt}
}

// Fields are the writable fields of a note.
type Fields struct {
	Title string `json:"title" minLength:"1" maxLength:"200" doc:"Note title"`
	Body  string `json:"body" maxLength:"10000" required:"false" doc:"Note text"`
}

// CreatedEmail is sent after a note is created.
var CreatedEmail = jobs.Define[CreatedEmailPayload]("notes:created_email")

// CreatedEmailPayload is the job payload for CreatedEmail.
type CreatedEmailPayload struct {
	To     string    `json:"to"`
	NoteID uuid.UUID `json:"noteId"`
	Title  string    `json:"title"`
}

// Service holds note business rules. All methods act for the caller in ctx.
type Service struct {
	q     dbgen.Querier
	queue jobs.Queue
	log   *slog.Logger
}

// NewService builds a Service.
func NewService(q dbgen.Querier, queue jobs.Queue, log *slog.Logger) *Service {
	return &Service{q: q, queue: queue, log: log}
}

// Create stores a note and queues a confirmation email.
func (s *Service) Create(ctx context.Context, f Fields) (Note, error) {
	p := auth.MustFromContext(ctx)
	row, err := s.q.CreateNote(ctx, dbgen.CreateNoteParams{OwnerID: p.Subject, Title: f.Title, Body: f.Body})
	if err != nil {
		return Note{}, err
	}
	if p.Email != "" {
		// The email is a best-effort side effect: the note exists, so a
		// queue outage must not fail the request.
		err := CreatedEmail.Enqueue(ctx, s.queue, CreatedEmailPayload{To: p.Email, NoteID: row.ID, Title: row.Title})
		if err != nil {
			s.log.WarnContext(ctx, "queue created-note email", "error", err, "note_id", row.ID)
		}
	}
	return fromRow(row), nil
}

// Get returns one of the caller's notes.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Note, error) {
	row, err := s.q.GetNote(ctx, dbgen.GetNoteParams{ID: id, OwnerID: auth.MustFromContext(ctx).Subject})
	return fromRow(row), err
}

// List returns a page of the caller's notes, newest first.
func (s *Service) List(ctx context.Context, limit, offset int32) ([]Note, error) {
	rows, err := s.q.ListNotes(ctx, dbgen.ListNotesParams{OwnerID: auth.MustFromContext(ctx).Subject, RowLimit: limit, RowOffset: offset})
	out := make([]Note, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, err
}

// Update replaces a note's fields.
func (s *Service) Update(ctx context.Context, id uuid.UUID, f Fields) (Note, error) {
	row, err := s.q.UpdateNote(ctx, dbgen.UpdateNoteParams{ID: id, OwnerID: auth.MustFromContext(ctx).Subject, Title: f.Title, Body: f.Body})
	return fromRow(row), err
}

// Delete removes a note.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteNote(ctx, dbgen.DeleteNoteParams{ID: id, OwnerID: auth.MustFromContext(ctx).Subject})
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.NotFound("note not found")
	}
	return nil
}

// RegisterJobs adds this feature's job handlers.
func RegisterJobs(reg *jobs.Registry, sender email.Sender) {
	jobs.Handle(reg, CreatedEmail, func(ctx context.Context, p CreatedEmailPayload) error {
		msg, err := email.Render("note_created", []string{p.To}, p)
		if err != nil {
			return jobs.Permanent(err)
		}
		return sender.Send(ctx, msg)
	})
}
