package notes

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/ShammiAnand/go-service-template/internal/httpapi"
)

// Request and response types. Huma builds the OpenAPI schema and request
// validation from these struct tags; handlers receive valid input.

type idInput struct {
	ID uuid.UUID `path:"id" format:"uuid" doc:"Note id"`
}

type createInput struct {
	Body Fields
}

// Huma skips unexported embedded structs, so path params are listed
// explicitly instead of embedding idInput.
type updateInput struct {
	ID   uuid.UUID `path:"id" format:"uuid" doc:"Note id"`
	Body Fields
}

type listInput struct {
	Limit  int32 `query:"limit" default:"20" minimum:"1" maximum:"100"`
	Offset int32 `query:"offset" default:"0" minimum:"0" maximum:"100000"`
}

type noteOutput struct {
	Body Note
}

type listOutput struct {
	Body struct {
		Items []Note `json:"items"`
	}
}

// Routes registers the notes operations.
func Routes(api huma.API, svc *Service) {
	tags := []string{"notes"}

	httpapi.Register(api, huma.Operation{
		OperationID: "createNote", Method: http.MethodPost, Path: "/v1/notes",
		Summary: "Create a note", Tags: tags, Security: httpapi.BearerAuth,
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createInput) (*noteOutput, error) {
		n, err := svc.Create(ctx, in.Body)
		return &noteOutput{Body: n}, err
	})

	httpapi.Register(api, huma.Operation{
		OperationID: "listNotes", Method: http.MethodGet, Path: "/v1/notes",
		Summary: "List your notes, newest first", Tags: tags, Security: httpapi.BearerAuth,
	}, func(ctx context.Context, in *listInput) (*listOutput, error) {
		items, err := svc.List(ctx, in.Limit, in.Offset)
		out := &listOutput{}
		out.Body.Items = items
		return out, err
	})

	httpapi.Register(api, huma.Operation{
		OperationID: "getNote", Method: http.MethodGet, Path: "/v1/notes/{id}",
		Summary: "Get a note", Tags: tags, Security: httpapi.BearerAuth,
	}, func(ctx context.Context, in *idInput) (*noteOutput, error) {
		n, err := svc.Get(ctx, in.ID)
		return &noteOutput{Body: n}, err
	})

	httpapi.Register(api, huma.Operation{
		OperationID: "updateNote", Method: http.MethodPut, Path: "/v1/notes/{id}",
		Summary: "Replace a note", Tags: tags, Security: httpapi.BearerAuth,
	}, func(ctx context.Context, in *updateInput) (*noteOutput, error) {
		n, err := svc.Update(ctx, in.ID, in.Body)
		return &noteOutput{Body: n}, err
	})

	httpapi.Register(api, huma.Operation{
		OperationID: "deleteNote", Method: http.MethodDelete, Path: "/v1/notes/{id}",
		Summary: "Delete a note", Tags: tags, Security: httpapi.BearerAuth,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		return nil, svc.Delete(ctx, in.ID)
	})
}
