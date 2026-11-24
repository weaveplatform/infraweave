package awsfake

import (
	"cmp"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// Fault is how the fake answers a request it fails.
type Fault int

const (
	// FaultNone answers the request.
	FaultNone Fault = iota
	// FaultServerError answers 500 InternalFailure.
	FaultServerError
	// FaultThrottle answers 400 Throttling, STS's rate limit.
	FaultThrottle
	// FaultMalformed answers 200 with a body that is not XML.
	FaultMalformed
	// FaultTimeout answers nothing until the request's context ends.
	FaultTimeout
)

// The bounds STS puts on a session: DurationSeconds from 900 to 43200, an
// hour when the request names none and a role's maximum when it sets none,
// and a session name of at most 64 characters from AWS's set.
const (
	minDuration     = 15 * time.Minute
	maxDuration     = 12 * time.Hour
	defaultDuration = time.Hour
	maxSessionName  = 64
)

var sessionName = regexp.MustCompile(`^[\w+=,.@-]*$`)

// Role is an IAM role callers assume.
type Role struct {
	// ARN is arn:aws:iam::<account>:role/<name>, with an optional path
	// before the name.
	ARN string
	// Trusted are the account IDs and principal ARNs the trust policy
	// names; arn:aws:iam::<account>:root trusts the account.
	Trusted []string
	// ExternalID is the sts:ExternalId condition, matched as StringLike
	// matches: * is any run of characters and ? one. Empty sets no
	// condition.
	ExternalID string
	// MaxDuration is the role's maximum session duration, an hour when
	// zero.
	MaxDuration time.Duration
}

// Call is a request as the fake received and answered it. Duration is the
// DurationSeconds the request named, zero when it named none; Region is
// the region of the SigV4 credential scope; Status and Code are the
// answer's HTTP status and STS error code, Code empty on success and
// Status zero for a FaultTimeout request. Issued are the credentials a
// successful AssumeRole answered, zero for any other call.
type Call struct {
	Action      string
	AccessKeyID string
	RoleARN     string
	ExternalID  string
	SessionName string
	Duration    time.Duration
	Tags        map[string]string
	Region      string
	Status      int
	Code        string
	Issued      Issued
}

// Issued are the temporary credentials an AssumeRole answered.
type Issued struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

type caller struct {
	account, arn, userID string
}

// Fake is AWS STS's AssumeRole and GetCallerIdentity over the awsQuery
// protocol at POST /, or GET / with the parameters in the query.
type Fake struct {
	router chi.Router

	mu         sync.Mutex
	callers    map[string]caller
	roles      map[string]Role
	calls      []Call
	next       Fault
	nextN      int
	roleFaults map[string]Fault
}

// New returns a fake with no caller and no role.
func New() *Fake {
	f := &Fake{callers: map[string]caller{}, roles: map[string]Role{}, roleFaults: map[string]Fault{}}
	r := chi.NewRouter()
	r.Post("/", f.serve)
	r.Get("/", f.serve)
	f.router = r
	return f
}

// ServeHTTP answers an STS request.
func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) { f.router.ServeHTTP(w, r) }

// Handler is the fake as an http.Handler.
func (f *Fake) Handler() http.Handler { return f }

// AddCaller registers the identity whose requests are signed with
// accessKeyID: an IAM principal arn in account.
func (f *Fake) AddCaller(accessKeyID, account, arn string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callers[accessKeyID] = caller{account: account, arn: arn, userID: "AIDA" + random(17)}
}

// AddRole registers r, replacing a role with its ARN.
func (f *Fake) AddRole(r Role) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.Trusted = slices.Clone(r.Trusted)
	f.roles[r.ARN] = r
}

// FailNext answers the next n requests, of any action, with fault.
func (f *Fake) FailNext(fault Fault, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next, f.nextN = fault, n
}

// FailRole answers every AssumeRole of roleARN with fault until FailRole
// sets FaultNone. FailNext's faults come first.
func (f *Fake) FailRole(roleARN string, fault Fault) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fault == FaultNone {
		delete(f.roleFaults, roleARN)
		return
	}
	f.roleFaults[roleARN] = fault
}

// Calls returns a copy of every request answered since New or Reset,
// faulted ones among them, in arrival order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.calls)
	for i := range out {
		out[i].Tags = maps.Clone(out[i].Tags)
	}
	return out
}

// Reset forgets the recorded calls and pending faults, keeping callers and
// roles.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
	f.next, f.nextN = FaultNone, 0
	clear(f.roleFaults)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedQueryString", "The request body is not a form.")
		return
	}
	c := parseCall(r)
	fault := f.fault(c)
	if fault != FaultNone {
		f.answerFault(w, r, c, fault)
		return
	}
	var status int
	var code string
	switch c.Action {
	case "GetCallerIdentity":
		status, code = f.callerIdentity(w, c)
	case "AssumeRole":
		status, code, c.Issued = f.assumeRole(w, c, r.Form.Has("DurationSeconds"))
	default:
		status, code = http.StatusBadRequest, "InvalidAction"
		writeError(w, status, code, fmt.Sprintf("Could not find operation %s for version 2011-06-15", c.Action))
	}
	c.Status, c.Code = status, code
	f.record(c)
}

// fault takes the fault the request meets, counting it off FailNext's.
func (f *Fake) fault(c Call) Fault {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nextN > 0 {
		f.nextN--
		return f.next
	}
	if c.Action == "AssumeRole" {
		return f.roleFaults[c.RoleARN]
	}
	return FaultNone
}

func (f *Fake) answerFault(w http.ResponseWriter, r *http.Request, c Call, fault Fault) {
	switch fault {
	case FaultServerError:
		c.Status, c.Code = http.StatusInternalServerError, "InternalFailure"
		writeError(w, c.Status, c.Code, "An internal error occurred.")
	case FaultThrottle:
		c.Status, c.Code = http.StatusBadRequest, "Throttling"
		writeError(w, c.Status, c.Code, "Rate exceeded")
	case FaultMalformed:
		c.Status = http.StatusOK
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(c.Status)
		_, _ = w.Write([]byte("<Response><Result><Unclosed"))
	case FaultTimeout:
		// Recorded before the hang, so a test sees the call while its
		// client still waits.
		f.record(c)
		<-r.Context().Done()
		return
	case FaultNone:
	}
	f.record(c)
}

func (f *Fake) record(c Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

// parseCall reads the action, its parameters and the SigV4 credential
// scope, Credential=<access key>/<date>/<region>/sts/aws4_request. The
// signature is not checked.
func parseCall(r *http.Request) Call {
	c := Call{
		Action:      r.Form.Get("Action"),
		RoleARN:     r.Form.Get("RoleArn"),
		ExternalID:  r.Form.Get("ExternalId"),
		SessionName: r.Form.Get("RoleSessionName"),
	}
	if s, err := strconv.Atoi(r.Form.Get("DurationSeconds")); err == nil {
		c.Duration = time.Duration(s) * time.Second
	}
	for i := 1; ; i++ {
		prefix := "Tags.member." + strconv.Itoa(i) + "."
		if !r.Form.Has(prefix + "Key") {
			break
		}
		if c.Tags == nil {
			c.Tags = map[string]string{}
		}
		c.Tags[r.Form.Get(prefix+"Key")] = r.Form.Get(prefix + "Value")
	}
	_, scope, ok := strings.Cut(r.Header.Get("Authorization"), "Credential=")
	if !ok {
		return c
	}
	scope, _, _ = strings.Cut(scope, ",")
	parts := strings.Split(scope, "/")
	c.AccessKeyID = parts[0]
	if len(parts) > 2 {
		c.Region = parts[2]
	}
	return c
}

func (f *Fake) callerIdentity(w http.ResponseWriter, c Call) (int, string) {
	f.mu.Lock()
	who, ok := f.callers[c.AccessKeyID]
	f.mu.Unlock()
	if !ok {
		return invalidToken(w)
	}
	writeResult(w, "GetCallerIdentity", element("Arn", who.arn)+element("UserId", who.userID)+element("Account", who.account))
	return http.StatusOK, ""
}

func (f *Fake) assumeRole(w http.ResponseWriter, c Call, durationSet bool) (int, string, Issued) {
	f.mu.Lock()
	who, known := f.callers[c.AccessKeyID]
	role, exists := f.roles[c.RoleARN]
	f.mu.Unlock()
	if !known {
		status, code := invalidToken(w)
		return status, code, Issued{}
	}
	duration := c.Duration
	if !durationSet {
		duration = defaultDuration
	}
	if msg := validate(c, duration); msg != "" {
		writeError(w, http.StatusBadRequest, "ValidationError", msg)
		return http.StatusBadRequest, "ValidationError", Issued{}
	}
	if !exists || !trusts(role, who) || !externalIDMatches(role.ExternalID, c.ExternalID) {
		writeError(w, http.StatusForbidden, "AccessDenied",
			fmt.Sprintf("User: %s is not authorized to perform: sts:AssumeRole on resource: %s", who.arn, c.RoleARN))
		return http.StatusForbidden, "AccessDenied", Issued{}
	}
	if limit := cmp.Or(role.MaxDuration, defaultDuration); duration > limit {
		writeError(w, http.StatusBadRequest, "ValidationError", "The requested DurationSeconds exceeds the MaxSessionDuration set for this role.")
		return http.StatusBadRequest, "ValidationError", Issued{}
	}
	account, name := roleParts(role.ARN)
	assumed := fmt.Sprintf("arn:aws:sts::%s:assumed-role/%s/%s", account, name, c.SessionName)
	issued := Issued{AccessKeyID: "ASIA" + random(16), SecretAccessKey: random(40), SessionToken: random(120)}
	roleID := "AROA" + random(17)
	f.mu.Lock()
	f.callers[issued.AccessKeyID] = caller{account: account, arn: assumed, userID: roleID + ":" + c.SessionName}
	f.mu.Unlock()
	expires := time.Now().UTC().Add(duration).Format(time.RFC3339)
	writeResult(w, "AssumeRole",
		"<Credentials>"+element("AccessKeyId", issued.AccessKeyID)+element("SecretAccessKey", issued.SecretAccessKey)+
			element("SessionToken", issued.SessionToken)+element("Expiration", expires)+"</Credentials>"+
			"<AssumedRoleUser>"+element("AssumedRoleId", roleID+":"+c.SessionName)+element("Arn", assumed)+"</AssumedRoleUser>")
	return http.StatusOK, "", issued
}

// validate answers the message of STS's ValidationError for a request
// whose parameters break their constraints, or "".
func validate(c Call, duration time.Duration) string {
	switch {
	case c.RoleARN == "":
		return "1 validation error detected: Value null at 'roleArn' failed to satisfy constraint: Member must not be null"
	case c.SessionName == "":
		return "1 validation error detected: Value null at 'roleSessionName' failed to satisfy constraint: Member must not be null"
	case len(c.SessionName) > maxSessionName:
		return fmt.Sprintf("1 validation error detected: Value '%s' at 'roleSessionName' failed to satisfy constraint: "+
			"Member must have length less than or equal to %d", c.SessionName, maxSessionName)
	case !sessionName.MatchString(c.SessionName):
		return fmt.Sprintf("1 validation error detected: Value '%s' at 'roleSessionName' failed to satisfy constraint: "+
			`Member must satisfy regular expression pattern: [\w+=,.@-]*`, c.SessionName)
	case duration < minDuration:
		return fmt.Sprintf("1 validation error detected: Value '%d' at 'durationSeconds' failed to satisfy constraint: "+
			"Member must have value greater than or equal to %d", int(duration/time.Second), int(minDuration/time.Second))
	case duration > maxDuration:
		return fmt.Sprintf("1 validation error detected: Value '%d' at 'durationSeconds' failed to satisfy constraint: "+
			"Member must have value less than or equal to %d", int(duration/time.Second), int(maxDuration/time.Second))
	}
	return ""
}

func trusts(r Role, who caller) bool {
	for _, t := range r.Trusted {
		if t == who.account || t == who.arn || t == "arn:aws:iam::"+who.account+":root" {
			return true
		}
	}
	return false
}

func externalIDMatches(condition, id string) bool {
	if condition == "" {
		return true
	}
	return id != "" && like(condition, id)
}

// like matches s against IAM's StringLike pattern p: * matches any run of
// characters, ? any one, and every other character itself.
func like(p, s string) bool {
	star, mark := -1, 0
	i, j := 0, 0
	for j < len(s) {
		switch {
		case i < len(p) && (p[i] == '?' || p[i] == s[j]):
			i++
			j++
		case i < len(p) && p[i] == '*':
			star, mark = i, j
			i++
		case star >= 0:
			mark++
			i, j = star+1, mark
		default:
			return false
		}
	}
	for i < len(p) && p[i] == '*' {
		i++
	}
	return i == len(p)
}

// roleParts splits arn:aws:iam::<account>:role/<path/><name> into the
// account and the name.
func roleParts(arn string) (account, name string) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) == 6 {
		account = parts[4]
		name = parts[5][strings.LastIndex(parts[5], "/")+1:]
	}
	return account, name
}

func invalidToken(w http.ResponseWriter) (int, string) {
	writeError(w, http.StatusForbidden, "InvalidClientTokenId", "The security token included in the request is invalid.")
	return http.StatusForbidden, "InvalidClientTokenId"
}

const namespace = "https://sts.amazonaws.com/doc/2011-06-15/"

func writeResult(w http.ResponseWriter, action, result string) {
	w.Header().Set("Content-Type", "text/xml")
	id := requestID()
	w.Header().Set("X-Amzn-Requestid", id)
	body := "<" + action + "Response xmlns=\"" + namespace + "\"><" + action + "Result>" + result + "</" + action + "Result>" +
		"<ResponseMetadata>" + element("RequestId", id) + "</ResponseMetadata></" + action + "Response>"
	_, _ = w.Write([]byte(body))
}

// writeError answers STS's error shape; a 5xx is AWS's fault and a 4xx
// the caller's, as Type says.
func writeError(w http.ResponseWriter, status int, code, message string) {
	kind := "Sender"
	if status >= http.StatusInternalServerError {
		kind = "Receiver"
	}
	id := requestID()
	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("X-Amzn-Requestid", id)
	w.WriteHeader(status)
	body := "<ErrorResponse xmlns=\"" + namespace + "\"><Error>" + element("Type", kind) + element("Code", code) +
		element("Message", message) + "</Error>" + element("RequestId", id) + "</ErrorResponse>"
	_, _ = w.Write([]byte(body))
}

func element(name, value string) string {
	var b strings.Builder
	b.WriteString("<" + name + ">")
	_ = xml.EscapeText(&b, []byte(value)) // a strings.Builder never fails a write
	b.WriteString("</" + name + ">")
	return b.String()
}

func requestID() string {
	t := strings.ToLower(rand.Text())
	return t[:8] + "-" + t[8:12] + "-" + t[12:16] + "-" + t[16:20] + "-" + t[20:26]
}

// random is n characters of base32 from crypto/rand, so no two sessions
// share keys.
func random(n int) string {
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(rand.Text())
	}
	return b.String()[:n]
}
