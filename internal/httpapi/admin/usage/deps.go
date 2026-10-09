package usage

import (
	adminshared "ds2api/internal/httpapi/admin/shared"
	"ds2api/internal/usagestats"
)

type Handler struct {
	Usage *usagestats.Store
}

var writeJSON = adminshared.WriteJSON
