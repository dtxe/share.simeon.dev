package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"share/backend/internal/auth"
	"share/backend/internal/store"
)

func normalizeTaxable(v *bool) bool {
	return v == nil || *v
}

func storeErrToStatus(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	log.Printf("store error: %v", err)
	writeJSONError(w, http.StatusInternalServerError, "internal error")
}

func requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return "", false
	}
	return userID, true
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	ip := auth.ClientIP(r, s.Cfg.TrustedProxy, s.Cfg.RealIPHeader)
	if allowed, err := s.RL.AllowCreateSessionPerIP(r.Context(), ip); err == nil && !allowed {
		writeJSONError(w, http.StatusTooManyRequests, "too many requests")
		return
	}

	sess, err := s.Store.CreateSession(r.Context(), userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sessionSummary(*sess))
}

type sessionSummaryDTO struct {
	ID             string     `json:"id"`
	Title          *string    `json:"title"`
	RestaurantName *string    `json:"restaurantName"`
	BillDate       *time.Time `json:"billDate"`
	SubtotalCents  int64      `json:"subtotalCents"`
	TaxCents       *int64     `json:"taxCents"`
	TotalPaidCents *int64     `json:"totalPaidCents"`
	HasReceipt     bool       `json:"hasReceipt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

func sessionSummary(b store.BillSession) sessionSummaryDTO {
	return sessionSummaryDTO{
		ID:             b.ID,
		Title:          b.Title,
		RestaurantName: b.RestaurantName,
		BillDate:       b.BillDate,
		SubtotalCents:  b.SubtotalCents,
		TaxCents:       b.TaxCents,
		TotalPaidCents: b.TotalPaidCents,
		HasReceipt:     b.ReceiptImagePath != nil,
		CreatedAt:      b.CreatedAt,
		UpdatedAt:      b.UpdatedAt,
	}
}

func (s *Server) handleListMyBills(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	sessions, err := s.Store.ListSessionsByOwner(r.Context(), userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	out := make([]sessionSummaryDTO, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, sessionSummary(sess))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	sess, err := s.Store.GetSession(r.Context(), id, userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	people, err := s.Store.ListPeople(r.Context(), id, userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	dishes, err := s.Store.ListDishes(r.Context(), id, userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	portions, err := s.Store.ListPortions(r.Context(), id, userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session":  sessionSummary(*sess),
		"people":   people,
		"dishes":   dishes,
		"portions": portions,
	})
}

type updateSessionBody struct {
	Title          *string    `json:"title"`
	RestaurantName *string    `json:"restaurantName"`
	BillDate       *time.Time `json:"billDate"`
	TotalPaidCents *int64     `json:"totalPaidCents"`
	TaxCents       *int64     `json:"taxCents"`
}

func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	var body updateSessionBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.TotalPaidCents != nil && (*body.TotalPaidCents < 0 || *body.TotalPaidCents > 5_000_000_00) {
		writeJSONError(w, http.StatusBadRequest, "totalPaidCents out of range")
		return
	}
	if body.TaxCents != nil && (*body.TaxCents < 0 || *body.TaxCents > 500_000_000) {
		writeJSONError(w, http.StatusBadRequest, "taxCents out of range")
		return
	}
	if body.Title != nil && len(*body.Title) > 120 {
		writeJSONError(w, http.StatusBadRequest, "title too long")
		return
	}

	err := s.Store.UpdateSession(r.Context(), id, userID, store.SessionPatch{
		Title:          body.Title,
		RestaurantName: body.RestaurantName,
		BillDate:       body.BillDate,
		TotalPaidCents: body.TotalPaidCents,
		TaxCents:       body.TaxCents,
	})
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type addPersonBody struct {
	Name string `json:"name"`
}

func (s *Server) handleAddPerson(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "id")

	var body addPersonBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || len(body.Name) < 1 || len(body.Name) > 50 {
		writeJSONError(w, http.StatusBadRequest, "name must be 1-50 characters")
		return
	}

	person, err := s.Store.AddPerson(r.Context(), sessionID, userID, body.Name)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, person)
}

func (s *Server) handleDeletePerson(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeletePerson(r.Context(), chi.URLParam(r, "personId"), userID); err != nil {
		storeErrToStatus(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type renamePersonBody struct {
	Name string `json:"name"`
}

func (s *Server) handleRenamePerson(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body renamePersonBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || len(body.Name) < 1 || len(body.Name) > 50 {
		writeJSONError(w, http.StatusBadRequest, "name must be 1-50 characters")
		return
	}
	if err := s.Store.RenamePerson(r.Context(), chi.URLParam(r, "personId"), userID, body.Name); err != nil {
		storeErrToStatus(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type dishInput struct {
	Name           string `json:"name"`
	UnitPriceCents int64  `json:"unitPriceCents"`
	Source         string `json:"source"`
	Taxable        *bool  `json:"taxable"`
}

type replaceDishesBody struct {
	Dishes []dishInput `json:"dishes"`
}

func (s *Server) handleReplaceDishes(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "id")

	var body replaceDishesBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Dishes) > 100 {
		writeJSONError(w, http.StatusBadRequest, "too many dishes")
		return
	}
	newDishes := make([]store.NewDish, 0, len(body.Dishes))
	for _, d := range body.Dishes {
		if len(d.Name) < 1 || len(d.Name) > 100 {
			writeJSONError(w, http.StatusBadRequest, "dish name must be 1-100 characters")
			return
		}
		if d.UnitPriceCents < 0 || d.UnitPriceCents > 100_000_00 {
			writeJSONError(w, http.StatusBadRequest, "unitPriceCents out of range")
			return
		}
		newDishes = append(newDishes, store.NewDish{
			Name:           d.Name,
			UnitPriceCents: d.UnitPriceCents,
			Source:         d.Source,
			Taxable:        normalizeTaxable(d.Taxable),
		})
	}

	dishes, err := s.Store.ReplaceDishes(r.Context(), sessionID, userID, newDishes)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dishes)
}

// handleAddDish appends a single dish without touching any existing dish or
// its portions — unlike handleReplaceDishes (bulk, reserved for /extract),
// this is what the item editor uses for manual add-a-row.
func (s *Server) handleAddDish(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "id")

	var body dishInput
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Name) < 1 || len(body.Name) > 100 {
		writeJSONError(w, http.StatusBadRequest, "dish name must be 1-100 characters")
		return
	}
	if body.UnitPriceCents < 0 || body.UnitPriceCents > 100_000_00 {
		writeJSONError(w, http.StatusBadRequest, "unitPriceCents out of range")
		return
	}
	dish, err := s.Store.AddDish(r.Context(), sessionID, userID, store.NewDish{
		Name:           body.Name,
		UnitPriceCents: body.UnitPriceCents,
		Source:         "manual",
		Taxable:        normalizeTaxable(body.Taxable),
	})
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dish)
}

func (s *Server) handleDeleteDish(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteDish(r.Context(), chi.URLParam(r, "dishId"), userID); err != nil {
		storeErrToStatus(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type updateDishBody struct {
	Name           *string `json:"name"`
	UnitPriceCents *int64  `json:"unitPriceCents"`
	Taxable        *bool   `json:"taxable"`
}

func (s *Server) handleUpdateDish(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body updateDishBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.UnitPriceCents != nil && (*body.UnitPriceCents < 0 || *body.UnitPriceCents > 100_000_00) {
		writeJSONError(w, http.StatusBadRequest, "unitPriceCents out of range")
		return
	}
	if body.Name != nil && (len(*body.Name) < 1 || len(*body.Name) > 100) {
		writeJSONError(w, http.StatusBadRequest, "dish name must be 1-100 characters")
		return
	}
	if err := s.Store.UpdateDish(r.Context(), chi.URLParam(r, "dishId"), userID, body.Name, body.UnitPriceCents, body.Taxable); err != nil {
		storeErrToStatus(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type upsertPortionBody struct {
	DishID   string  `json:"dishId"`
	PersonID string  `json:"personId"`
	Shares   float64 `json:"shares"`
}

func (s *Server) handleUpsertPortion(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body upsertPortionBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Shares < 0 || body.Shares > 100 {
		writeJSONError(w, http.StatusBadRequest, "shares out of range")
		return
	}
	if err := s.Store.UpsertPortion(r.Context(), body.DishID, body.PersonID, userID, body.Shares); err != nil {
		storeErrToStatus(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleBreakdown(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	sess, result, err := s.Store.GetBreakdown(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		storeErrToStatus(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session": sessionSummary(*sess),
		"result":  result,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
