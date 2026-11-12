// Package entitlement verifies StoreKit 2 transactions (signed JWS) and App
// Store Server Notifications V2, maps devices to subscriptions, and enforces
// plan limits (Free/Plus) where the server must gate: alert areas and
// offline packs. Display gating stays in the app.
package entitlement
