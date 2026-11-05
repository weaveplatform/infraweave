// Package notify fans PostgreSQL notifications out within one replica
// (decision 0005). Each replica holds one Listener: a single connection,
// named ApplicationName, that LISTENs on every channel in Channels and hands
// each notification to the Subscriptions of its channel. Notify sends one,
// and inside a transaction PostgreSQL delivers it only on commit.
//
// A notification is a hint, never the data: a subscriber rereads the rows it
// cares about. Whenever the listener (re)connects, and whenever a slow
// subscriber's buffer overflows, the subscriber gets a Message with Resync
// set, since notifications sent while nobody listened are lost.
//
// # Input and output
//
// Run holds the listening connection until ctx ends, reconnecting with
// jittered backoff from 100 ms to 5 s, and logs each failed connection as a
// warning through the given slog.Logger. Notify runs pg_notify through the
// Execer it is given. Payloads are passed through as they are.
//
// # Dependencies
//
// It imports pgx and pgconn, and the standard library otherwise. It takes the
// connection settings from platform/sql's DB.ListenConfig and imports no
// infraweave package.
package notify
