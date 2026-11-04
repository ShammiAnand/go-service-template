package notes

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ShammiAnand/go-service-template/internal/auth"
	"github.com/ShammiAnand/go-service-template/internal/db/dbgen"
	"github.com/ShammiAnand/go-service-template/internal/email"
	"github.com/ShammiAnand/go-service-template/internal/jobs"
)

// memQuerier is an in-memory dbgen.Querier for unit tests. Integration
// tests cover the real SQL.
type memQuerier struct{ rows map[uuid.UUID]dbgen.Note }

func newMem() *memQuerier { return &memQuerier{rows: map[uuid.UUID]dbgen.Note{}} }

func (m *memQuerier) CreateNote(_ context.Context, a dbgen.CreateNoteParams) (dbgen.Note, error) {
	n := dbgen.Note{ID: uuid.New(), OwnerID: a.OwnerID, Title: a.Title, Body: a.Body, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	m.rows[n.ID] = n
	return n, nil
}

func (m *memQuerier) GetNote(_ context.Context, a dbgen.GetNoteParams) (dbgen.Note, error) {
	n, ok := m.rows[a.ID]
	if !ok || n.OwnerID != a.OwnerID {
		return dbgen.Note{}, pgx.ErrNoRows
	}
	return n, nil
}

func (m *memQuerier) ListNotes(_ context.Context, a dbgen.ListNotesParams) ([]dbgen.Note, error) {
	out := []dbgen.Note{}
	for _, n := range m.rows {
		if n.OwnerID == a.OwnerID {
			out = append(out, n)
		}
	}
	return out, nil
}

func (m *memQuerier) UpdateNote(ctx context.Context, a dbgen.UpdateNoteParams) (dbgen.Note, error) {
	n, err := m.GetNote(ctx, dbgen.GetNoteParams{ID: a.ID, OwnerID: a.OwnerID})
	if err != nil {
		return n, err
	}
	n.Title, n.Body = a.Title, a.Body
	m.rows[n.ID] = n
	return n, nil
}

func (m *memQuerier) DeleteNote(_ context.Context, a dbgen.DeleteNoteParams) (int64, error) {
	n, ok := m.rows[a.ID]
	if !ok || n.OwnerID != a.OwnerID {
		return 0, nil
	}
	delete(m.rows, a.ID)
	return 1, nil
}

type captureSender struct{ sent []email.Message }

func (c *captureSender) Send(_ context.Context, m email.Message) error {
	c.sent = append(c.sent, m)
	return nil
}

type failingQueue struct{}

func (failingQueue) Enqueue(context.Context, jobs.Task) error { return errors.New("redis down") }

func as(sub, mail string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Subject: sub, Email: mail})
}

func TestCreateSendsEmailJob(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	reg := jobs.NewRegistry(log)
	sender := &captureSender{}
	RegisterJobs(reg, sender)
	svc := NewService(newMem(), jobs.Inline{Registry: reg}, log)

	n, err := svc.Create(as("u1", "u1@example.com"), Fields{Title: "Hello"})
	require.NoError(t, err)
	require.Len(t, sender.sent, 1)
	assert.Equal(t, []string{"u1@example.com"}, sender.sent[0].To)
	assert.Contains(t, sender.sent[0].Text, n.ID.String())
}

func TestCreateSucceedsWhenQueueIsDown(t *testing.T) {
	svc := NewService(newMem(), failingQueue{}, slog.New(slog.DiscardHandler))
	_, err := svc.Create(as("u1", "u1@example.com"), Fields{Title: "Hello"})
	require.NoError(t, err)
}

func TestOwnerIsolation(t *testing.T) {
	svc := NewService(newMem(), failingQueue{}, slog.New(slog.DiscardHandler))
	n, err := svc.Create(as("alice", ""), Fields{Title: "secret"})
	require.NoError(t, err)

	_, err = svc.Get(as("bob", ""), n.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	err = svc.Delete(as("bob", ""), n.ID)
	require.Error(t, err)

	list, err := svc.List(as("bob", ""), 10, 0)
	require.NoError(t, err)
	assert.Empty(t, list)
	assert.NotNil(t, list, "empty list encodes as [] not null")
}
