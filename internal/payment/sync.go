package payment

import (
	"context"
	"log"
	"time"

	"github.com/stripe/stripe-go/v82"

	"pulse/internal/orders"
	"pulse/internal/users"
)

// SyncSubscriptions checks all active subscription orders against Stripe
// to catch any missed webhook events (cancellations, failures, etc.).
func SyncSubscriptions(ctx context.Context, orderStore orders.Store, userStore users.Store) error {
	allOrders, err := orderStore.ListOrders()
	if err != nil {
		return err
	}

	for _, order := range allOrders {
		if order.StripeSubscriptionID == "" || order.Status != orders.StatusPaid {
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		sub, err := GetSubscription(order.StripeSubscriptionID)
		if err != nil {
			log.Printf("sync-stripe: get subscription %s: %v", order.StripeSubscriptionID, err)
			continue
		}

		switch sub.Status {
		case stripe.SubscriptionStatusCanceled,
			stripe.SubscriptionStatusUnpaid,
			stripe.SubscriptionStatusIncompleteExpired:
			order.Status = orders.StatusFailed
			if _, err := orderStore.UpsertOrder(order); err != nil {
				log.Printf("sync-stripe: update order %s: %v", order.ID, err)
			}
			if order.UserID != "" {
				user, err := userStore.GetUser(order.UserID)
				if err == nil && user.Status == users.StatusActive {
					user.Status = users.StatusDisabled
					user.CurrentPlanID = ""
					if _, err := userStore.UpsertUser(user); err != nil {
						log.Printf("sync-stripe: disable user %s: %v", user.ID, err)
					} else {
						log.Printf("sync-stripe: disabled user %s (subscription %s cancelled)", user.Username, order.StripeSubscriptionID)
					}
				}
			}

		case stripe.SubscriptionStatusPastDue:
			log.Printf("sync-stripe: subscription %s is past_due for user %s", order.StripeSubscriptionID, order.UserID)

		case stripe.SubscriptionStatusActive, stripe.SubscriptionStatusTrialing:
			// In stripe-go v82, CurrentPeriodEnd is on each SubscriptionItem.
			var periodEnd int64
			if sub.Items != nil {
				for _, item := range sub.Items.Data {
					if item.CurrentPeriodEnd > periodEnd {
						periodEnd = item.CurrentPeriodEnd
					}
				}
			}
			if periodEnd > 0 && order.UserID != "" {
				newExpiry := time.Unix(periodEnd, 0).UTC()
				user, err := userStore.GetUser(order.UserID)
				if err == nil && (user.ExpireAt == nil || !user.ExpireAt.Equal(newExpiry)) {
					user.ExpireAt = &newExpiry
					if _, err := userStore.UpsertUser(user); err != nil {
						log.Printf("sync-stripe: update expiry for user %s: %v", user.ID, err)
					}
				}
			}
		}
	}
	return nil
}
