// Package queuetest is the queue's contract suite: TestAcc tests that run
// package queue against PostgreSQL, one database per test, for every case
// the queue's decision record lists to verify. make accept runs it in
// accept-core and, on the next PostgreSQL major, in postgres-next.
//
// ref: decision 0011
//
// # Input and output
//
// Its tests create jobs through package queue and read the jobs table and
// infraweave_jobs notifications directly; it has no exported API.
//
// # Dependencies
//
// Its tests import package queue, platform/tx, platform/sql/sqltest and the
// platform stream; nothing imports it.
package queuetest
