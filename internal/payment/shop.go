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
	// GET /shop/success is handled by panel handler (renders HTML template)
}

func (s *ShopAPI) listPlans(w http.ResponseWriter, r *http.Request) {
	list, err := s.PlanStore.ListEnabledPlans()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list plans")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *ShopAPI) createCheckout(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}
	planID := r.FormValue("plan_id")
	email := r.FormValue("email")
	subToken := r.FormValue("sub_token")
	if planID == "" || email == "" {
		http.Error(w, "plan_id and email are required", http.StatusBadRequest)
		return
	}

	plan, err := s.PlanStore.GetPlan(planID)
	if err != nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}
	if !plan.Enabled {
		http.Error(w, "plan is not available", http.StatusBadRequest)
		return
	}
	if plan.StripePriceID == "" {
		http.Error(w, "plan has no Stripe price configured", http.StatusBadRequest)
		return
	}

	orderID := idgen.NextString()

	var userID string
	if subToken != "" {
		user, err := s.UserStore.GetUserBySubToken(subToken)
		if err == nil {
			userID = user.ID
		}
	}

	order := orders.Order{
		ID:          orderID,
		UserID:      userID,
		PlanID:      plan.ID,
		Email:       email,
		Status:      orders.StatusPending,
		AmountCents: plan.PriceCents,
		Currency:    plan.Currency,
	}

	successURL := s.BaseURL + "/shop/success?session_id={CHECKOUT_SESSION_ID}"
	cancelURL := s.BaseURL + "/shop"

	sessionID, checkoutURL, err := CreateCheckoutSession(plan, email, orderID, subToken, successURL, cancelURL)
	if err != nil {
		log.Printf("payment: create checkout session: %v", err)
		http.Error(w, "failed to create checkout session", http.StatusInternalServerError)
		return
	}

	order.StripeSessionID = sessionID
	if _, err := s.OrderStore.UpsertOrder(order); err != nil {
		log.Printf("payment: save order %s: %v", orderID, err)
		http.Error(w, "failed to save order", http.StatusInternalServerError)
		return
	}

	// Redirect browser to Stripe Checkout
	http.Redirect(w, r, checkoutURL, http.StatusSeeOther)
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
