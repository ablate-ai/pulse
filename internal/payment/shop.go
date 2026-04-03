package payment

import (
	"encoding/json"
	"log"
	"net/http"

	"pulse/internal/idgen"
	"pulse/internal/orders"
	"pulse/internal/plans"
	"pulse/internal/users"
)

// ShopAPI handles public shop endpoints.
type ShopAPI struct {
	PlanStore  plans.Store
	OrderStore orders.Store
	UserStore  users.Store
	Deps       *WebhookDeps
	BaseURL    string // e.g., "https://example.com"
}

// Register registers shop routes on the given mux.
// These are PUBLIC endpoints (no auth required).
func (s *ShopAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /shop/plans", s.listPlans)
	mux.HandleFunc("POST /shop/checkout", s.createCheckout)
	mux.HandleFunc("GET /shop/success", s.successPage)
}

func (s *ShopAPI) listPlans(w http.ResponseWriter, r *http.Request) {
	list, err := s.PlanStore.ListEnabledPlans()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list plans")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type checkoutRequest struct {
	PlanID   string `json:"plan_id"`
	Email    string `json:"email"`
	SubToken string `json:"sub_token,omitempty"`
}

type checkoutResponse struct {
	CheckoutURL string `json:"checkout_url"`
}

func (s *ShopAPI) createCheckout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PlanID == "" || req.Email == "" {
		writeJSONError(w, http.StatusBadRequest, "plan_id and email are required")
		return
	}

	plan, err := s.PlanStore.GetPlan(req.PlanID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "plan not found")
		return
	}
	if !plan.Enabled {
		writeJSONError(w, http.StatusBadRequest, "plan is not available")
		return
	}
	if plan.StripePriceID == "" {
		writeJSONError(w, http.StatusBadRequest, "plan has no Stripe price configured")
		return
	}

	orderID := idgen.NextString()

	// If sub_token is provided, look up the existing user
	var userID string
	if req.SubToken != "" {
		user, err := s.UserStore.GetUserBySubToken(req.SubToken)
		if err == nil {
			userID = user.ID
		}
	}

	order := orders.Order{
		ID:          orderID,
		UserID:      userID,
		PlanID:      plan.ID,
		Email:       req.Email,
		Status:      orders.StatusPending,
		AmountCents: plan.PriceCents,
		Currency:    plan.Currency,
	}

	successURL := s.BaseURL + "/shop/success?session_id={CHECKOUT_SESSION_ID}"
	cancelURL := s.BaseURL + "/shop/plans"

	sessionID, checkoutURL, err := CreateCheckoutSession(plan, req.Email, orderID, req.SubToken, successURL, cancelURL)
	if err != nil {
		log.Printf("payment: create checkout session: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to create checkout session")
		return
	}

	order.StripeSessionID = sessionID
	if _, err := s.OrderStore.UpsertOrder(order); err != nil {
		log.Printf("payment: save order %s: %v", orderID, err)
		writeJSONError(w, http.StatusInternalServerError, "failed to save order")
		return
	}

	writeJSON(w, http.StatusOK, checkoutResponse{CheckoutURL: checkoutURL})
}

type successResponse struct {
	Message  string `json:"message"`
	SubURL   string `json:"sub_url,omitempty"`
	SubToken string `json:"sub_token,omitempty"`
}

func (s *ShopAPI) successPage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	order, err := s.OrderStore.GetOrderByStripeSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "order not found")
		return
	}

	resp := successResponse{
		Message: "Payment successful! Your account is being set up.",
	}

	if order.UserID != "" {
		user, err := s.UserStore.GetUser(order.UserID)
		if err == nil && user.SubToken != "" {
			resp.SubURL = s.BaseURL + "/sub/" + user.SubToken
			resp.SubToken = user.SubToken
			resp.Message = "Payment successful! Your subscription is active."
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("payment: write json: %v", err)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
