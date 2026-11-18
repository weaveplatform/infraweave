// Package deliveryfake sends HTTP webhook fixtures for the Bitbucket fakes.
//
// # Input and output
//
// A sender holds a cloned webhook secret and HTTP client, retaining each
// attempt's delivery ID, event, attempt number and response status. Send clones
// the body; Redeliver retains its original URL, ID and bytes. Cloud retries
// only 5xx responses, at most three total attempts; Data Center sends once.
// Missing secrets model unsigned hooks. No sender follows redirects, retries
// transport failures or logs its secret. Retry timing is not specified here.
//
// # Dependencies
//
// It imports only the standard library. The public Bitbucket fake packages
// select headers and retry policy; production adapters do not import it.
package deliveryfake
