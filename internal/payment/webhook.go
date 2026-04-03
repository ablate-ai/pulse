package payment

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stripe/stripe-go/v82"
	"pulse/internal/idgen"
	"pulse/internal/orders"
	"pulse/internal/plans"
	"pulse/internal/users"
)

// webhookMu serializes webhook processing to prevent race conditions
// on read-modify-write cycles (e.g., duplicate checkout.session.completed).
var webhookMu sync.Mutex

// WebhookDeps holds the dependencies for webhook processing.
type WebhookDeps struct {
	OrderStore    orders.Store
	PlanStore     plans.Store
	UserStore     users.Store
	WebhookSecret string
	// SyncUserInbounds is called after creating a user to assign inbounds.
	// It takes (userID, []inboundID) and returns (affectedNodeIDs, error).
	SyncUserInbounds func(userID string, inboundIDs []string) ([]string, error)
	// ApplyNodes pushes config to affected nodes.
	ApplyNodes func(nodeIDs []string)
}

// HandleWebhook is the HTTP handler for POST /webhook/stripe.
func (d *WebhookDeps) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	event, err := ConstructEvent(body, r.Header.Get("Stripe-Signature"), d.WebhookSecret)
	if err != nil {
		log.Printf("payment: webhook signature error (secret len=%d): %v", len(d.WebhookSecret), err)
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	switch event.Type {
	case "checkout.session.completed":
		webhookMu.Lock()
		d.handleCheckoutCompleted(event)
		webhookMu.Unlock()
	case "invoice.paid":
		webhookMu.Lock()
		d.handleInvoicePaid(event)
		webhookMu.Unlock()
	case "invoice.payment_failed":
		webhookMu.Lock()
		d.handleInvoicePaymentFailed(event)
		webhookMu.Unlock()
	case "customer.subscription.deleted":
		webhookMu.Lock()
		d.handleSubscriptionDeleted(event)
		webhookMu.Unlock()
	}

	w.WriteHeader(http.StatusOK)
}

func (d *WebhookDeps) handleCheckoutCompleted(event stripe.Event) {
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		log.Printf("payment: unmarshal checkout session: %v", err)
		return
	}

	order, err := d.OrderStore.GetOrderByStripeSession(sess.ID)
	if err != nil {
		log.Printf("payment: get order by session %s: %v", sess.ID, err)
		return
	}

	// Idempotency: skip if already processed
	if order.Status == orders.StatusPaid {
		return
	}

	now := time.Now().UTC()
	order.Status = orders.StatusPaid
	order.PaidAt = &now
	if sess.Customer != nil {
		order.StripeCustomerID = sess.Customer.ID
	}
	if sess.Subscription != nil {
		order.StripeSubscriptionID = sess.Subscription.ID
	}

	plan, err := d.PlanStore.GetPlan(order.PlanID)
	if err != nil {
		log.Printf("payment: get plan %s: %v", order.PlanID, err)
		return
	}

	if order.UserID == "" {
		// New user from shop purchase
		d.provisionNewUser(&order, plan, now)
	} else {
		// Existing user renewal
		d.renewExistingUser(order, plan, now)
	}

	if _, err := d.OrderStore.UpsertOrder(order); err != nil {
		log.Printf("payment: update order %s: %v", order.ID, err)
	}
}

func (d *WebhookDeps) provisionNewUser(order *orders.Order, plan plans.Plan, now time.Time) {
	username := emailToUsername(order.Email)

	// Check for username collision and append random suffix
	if _, err := d.findUserByUsername(username); err == nil {
		username = username + "-" + randomHex(3)
	}

	expireAt := now.Add(time.Duration(plan.DurationDays) * 24 * time.Hour)
	subToken := randomHex(16)

	newUser := users.User{
		ID:                     idgen.NextString(),
		Username:               username,
		Status:                 users.StatusActive,
		TrafficLimit:           plan.TrafficLimit,
		DataLimitResetStrategy: plan.DataLimitResetStrategy,
		ExpireAt:               &expireAt,
		CreatedAt:              now,
		SubToken:               subToken,
		StripeCustomerID:       order.StripeCustomerID,
		CurrentPlanID:          plan.ID,
		Email:                  order.Email,
	}

	if _, err := d.UserStore.UpsertUser(newUser); err != nil {
		log.Printf("payment: create user for order %s: %v", order.ID, err)
		return
	}

	order.UserID = newUser.ID

	// Assign inbounds from plan
	if plan.InboundIDs != "" {
		ibIDs := strings.Split(plan.InboundIDs, ",")
		for i := range ibIDs {
			ibIDs[i] = strings.TrimSpace(ibIDs[i])
		}
		affected, err := d.SyncUserInbounds(newUser.ID, ibIDs)
		if err != nil {
			log.Printf("payment: sync inbounds for user %s: %v", newUser.ID, err)
			return
		}
		d.ApplyNodes(affected)
	}
}

func (d *WebhookDeps) renewExistingUser(order orders.Order, plan plans.Plan, now time.Time) {
	user, err := d.UserStore.GetUser(order.UserID)
	if err != nil {
		log.Printf("payment: get user %s for renewal: %v", order.UserID, err)
		return
	}

	// Extend expiry
	base := now
	if user.ExpireAt != nil && user.ExpireAt.After(now) {
		base = *user.ExpireAt
	}
	expireAt := base.Add(time.Duration(plan.DurationDays) * 24 * time.Hour)
	user.ExpireAt = &expireAt

	if plan.TrafficLimit > 0 {
		user.TrafficLimit = plan.TrafficLimit
	}
	user.CurrentPlanID = plan.ID
	user.Status = users.StatusActive

	if _, err := d.UserStore.UpsertUser(user); err != nil {
		log.Printf("payment: update user %s for renewal: %v", user.ID, err)
	}
}

func (d *WebhookDeps) handleInvoicePaid(event stripe.Event) {
	var invoice struct {
		Subscription string `json:"subscription"`
		Customer     string `json:"customer"`
	}
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		log.Printf("payment: unmarshal invoice: %v", err)
		return
	}
	if invoice.Subscription == "" {
		return
	}

	order, err := d.OrderStore.GetOrderByStripeSubscription(invoice.Subscription)
	if err != nil {
		log.Printf("payment: get order by subscription %s: %v", invoice.Subscription, err)
		return
	}
	if order.UserID == "" {
		return
	}

	plan, err := d.PlanStore.GetPlan(order.PlanID)
	if err != nil {
		log.Printf("payment: get plan %s for invoice: %v", order.PlanID, err)
		return
	}

	user, err := d.UserStore.GetUser(order.UserID)
	if err != nil {
		log.Printf("payment: get user %s for invoice: %v", order.UserID, err)
		return
	}

	now := time.Now().UTC()
	base := now
	if user.ExpireAt != nil && user.ExpireAt.After(now) {
		base = *user.ExpireAt
	}
	expireAt := base.Add(time.Duration(plan.DurationDays) * 24 * time.Hour)
	user.ExpireAt = &expireAt
	user.Status = users.StatusActive

	// Reset traffic on renewal
	user.UploadBytes = 0
	user.DownloadBytes = 0
	user.UsedBytes = 0

	if _, err := d.UserStore.UpsertUser(user); err != nil {
		log.Printf("payment: update user %s for invoice: %v", user.ID, err)
	}
}

func (d *WebhookDeps) handleInvoicePaymentFailed(event stripe.Event) {
	var invoice struct {
		Customer string `json:"customer"`
	}
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		log.Printf("payment: unmarshal invoice failed event: %v", err)
		return
	}
	if invoice.Customer == "" {
		return
	}

	user, err := d.UserStore.GetUserByStripeCustomerID(invoice.Customer)
	if err != nil {
		log.Printf("payment: get user by customer %s: %v", invoice.Customer, err)
		return
	}

	user.Status = users.StatusOnHold
	if _, err := d.UserStore.UpsertUser(user); err != nil {
		log.Printf("payment: set user %s on_hold: %v", user.ID, err)
	}
}

func (d *WebhookDeps) handleSubscriptionDeleted(event stripe.Event) {
	var sub struct {
		Customer string `json:"customer"`
	}
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		log.Printf("payment: unmarshal subscription deleted event: %v", err)
		return
	}
	if sub.Customer == "" {
		return
	}

	user, err := d.UserStore.GetUserByStripeCustomerID(sub.Customer)
	if err != nil {
		log.Printf("payment: get user by customer %s: %v", sub.Customer, err)
		return
	}

	user.Status = users.StatusDisabled
	if _, err := d.UserStore.UpsertUser(user); err != nil {
		log.Printf("payment: disable user %s: %v", user.ID, err)
	}
}

// findUserByUsername searches for a user by username (linear scan).
func (d *WebhookDeps) findUserByUsername(username string) (users.User, error) {
	all, err := d.UserStore.ListUsers()
	if err != nil {
		return users.User{}, err
	}
	for _, u := range all {
		if u.Username == username {
			return u, nil
		}
	}
	return users.User{}, users.ErrUserNotFound
}

func emailToUsername(email string) string {
	parts := strings.SplitN(email, "@", 2)
	name := parts[0]
	// Sanitize: keep only alphanumeric, dash, underscore, dot
	var b strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			b.WriteRune(c)
		}
	}
	result := b.String()
	if result == "" {
		result = "user-" + randomHex(4)
	}
	return result
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", buf)
}
