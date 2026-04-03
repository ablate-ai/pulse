package payment

import (
	"fmt"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/subscription"
	"github.com/stripe/stripe-go/v82/webhook"
	"pulse/internal/plans"
)

// Init sets the Stripe API key. Call once at startup.
func Init(secretKey string) {
	stripe.Key = secretKey
}

// CreateCheckoutSession creates a Stripe Checkout session for the given plan.
// Returns (sessionID, checkoutURL, error).
func CreateCheckoutSession(plan plans.Plan, email string, orderID string, subToken string, successURL string, cancelURL string) (string, string, error) {
	mode := stripe.String(string(stripe.CheckoutSessionModePayment))
	if plan.Type == plans.TypeSubscription {
		mode = stripe.String(string(stripe.CheckoutSessionModeSubscription))
	}

	params := &stripe.CheckoutSessionParams{
		Mode:          mode,
		SuccessURL:    stripe.String(successURL),
		CancelURL:     stripe.String(cancelURL),
		CustomerEmail: stripe.String(email),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(plan.StripePriceID),
				Quantity: stripe.Int64(1),
			},
		},
	}
	params.AddMetadata("order_id", orderID)
	params.AddMetadata("plan_id", plan.ID)
	if subToken != "" {
		params.AddMetadata("sub_token", subToken)
	}

	s, err := session.New(params)
	if err != nil {
		return "", "", fmt.Errorf("create checkout session: %w", err)
	}
	return s.ID, s.URL, nil
}

// ConstructEvent verifies a webhook payload signature and returns the event.
func ConstructEvent(payload []byte, sigHeader string, webhookSecret string) (stripe.Event, error) {
	return webhook.ConstructEventWithOptions(payload, sigHeader, webhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
}

// CancelSubscription cancels a Stripe subscription immediately.
func CancelSubscription(subID string) error {
	_, err := subscription.Cancel(subID, nil)
	return err
}

// GetSubscription retrieves a Stripe subscription by ID.
func GetSubscription(subID string) (*stripe.Subscription, error) {
	return subscription.Get(subID, nil)
}
