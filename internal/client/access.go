package client

import (
	"context"

	"github.com/google/uuid"
)

// AccessClient is implemented by back/access's generated HTTP client
// (github.com/mbatimel/AMC/access/pkg/client/transport.ClientAccessAPI).
type AccessClient interface {
	CheckAccess(ctx context.Context, userID uuid.UUID, role int) (allowed bool, err error)
}
