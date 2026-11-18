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
// infraweave_jobs notifications directly. ConsumeTwice is the reusable
// fenced-consumer contract: a domain supplies its handler on a real queue
// and checks its own target rows after both claimed jobs complete.
//
// # Dependencies
//
// ConsumeTwice imports kernel and queue. Its tests import platform/tx,
// platform/sql/sqltest and the platform stream. Domain contract suites
// import the reusable consumer contract; production code does not.
package queuetest
