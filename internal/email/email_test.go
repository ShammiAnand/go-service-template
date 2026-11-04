package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderNoteCreated(t *testing.T) {
	m, err := Render("note_created", []string{"a@example.com"}, map[string]string{
		"Title":  `<script>x</script>`,
		"NoteID": "123",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a@example.com"}, m.To)
	assert.Equal(t, "New note: <script>x</script>", m.Subject)
	assert.Contains(t, m.Text, "Note id: 123")
	assert.Contains(t, m.HTML, "&lt;script&gt;", "HTML is escaped")
	assert.NotContains(t, m.HTML, "<script>")
}

func TestRenderUnknownTemplate(t *testing.T) {
	_, err := Render("nope", nil, nil)
	require.Error(t, err)
}
