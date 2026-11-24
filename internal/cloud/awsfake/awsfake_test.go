package awsfake_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/aws/smithy-go"

	"github.com/weaveplatform/infraweave/internal/cloud/awsfake"
)

// SigV4 keys its HMAC with "AWS4" and the secret, and FIPS 140-only mode
// refuses an HMAC key under 112 bits, so the secret is as long as a real
// one.
const (
	serverKey    = "AKIASERVER0000000001"
	strangerKey  = "AKIASTRANGER00000001"
	secret       = "infraweave-test-secret-0123456789abcdefghi" //nolint:gosec // a test credential
	serverAcct   = "111111111111"
	serverARN    = "arn:aws:iam::111111111111:user/infraweave"
	customerAcct = "222222222222"
	roleARN      = "arn:aws:iam::222222222222:role/deploy/infraweave"
)

func setup(t *testing.T) (*awsfake.Fake, *sts.Client) {
	t.Helper()
	f := awsfake.New()
	f.AddCaller(serverKey, serverAcct, serverARN)
	f.AddRole(awsfake.Role{ARN: roleARN, Trusted: []string{serverAcct}, ExternalID: "acme@*@prod@?"})
	srv := httptest.NewServer(f.Handler())
	t.Cleanup(srv.Close)
	return f, client(srv.URL, serverKey)
}

func client(base, key string) *sts.Client {
	return sts.New(sts.Options{
		Region:           "eu-west-1",
		BaseEndpoint:     aws.String(base),
		Credentials:      credentials.NewStaticCredentialsProvider(key, secret, ""),
		RetryMaxAttempts: 1,
	})
}

func code(t *testing.T, err error) string {
	t.Helper()
	var api smithy.APIError
	if !errors.As(err, &api) {
		t.Fatalf("error %v is not an STS error", err)
	}
	return api.ErrorCode()
}

func assume(ctx context.Context, c *sts.Client, external string, mut ...func(*sts.AssumeRoleInput)) (*sts.AssumeRoleOutput, error) {
	in := &sts.AssumeRoleInput{RoleArn: aws.String(roleARN), RoleSessionName: aws.String("infraweave@stack"), ExternalId: aws.String(external)}
	if external == "" {
		in.ExternalId = nil
	}
	for _, m := range mut {
		m(in)
	}
	return c.AssumeRole(ctx, in)
}

func TestUnitCallerIdentity(t *testing.T) {
	f, c := setup(t)
	out, err := c.GetCallerIdentity(t.Context(), &sts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(out.Account) != serverAcct || aws.ToString(out.Arn) != serverARN || !strings.HasPrefix(aws.ToString(out.UserId), "AIDA") {
		t.Fatalf("identity = %s %s %s", aws.ToString(out.Account), aws.ToString(out.Arn), aws.ToString(out.UserId))
	}
	calls := f.Calls()
	if len(calls) != 1 || calls[0].Action != "GetCallerIdentity" || calls[0].AccessKeyID != serverKey ||
		calls[0].Region != "eu-west-1" || calls[0].Status != http.StatusOK || calls[0].Code != "" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUnitUnknownCaller(t *testing.T) {
	f, _ := setup(t)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := client(srv.URL, strangerKey)
	if _, err := c.GetCallerIdentity(t.Context(), &sts.GetCallerIdentityInput{}); code(t, err) != "InvalidClientTokenId" {
		t.Fatalf("GetCallerIdentity = %v", err)
	}
	if _, err := assume(t.Context(), c, "acme@x@prod@r"); code(t, err) != "InvalidClientTokenId" {
		t.Fatalf("AssumeRole = %v", err)
	}
	if calls := f.Calls(); len(calls) != 2 || calls[1].Status != http.StatusForbidden {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUnitAssumeRole(t *testing.T) {
	f, c := setup(t)
	out, err := assume(t.Context(), c, "acme@01J@prod@r", func(in *sts.AssumeRoleInput) {
		in.DurationSeconds = aws.Int32(1800)
		in.Tags = []types.Tag{{Key: aws.String("account"), Value: aws.String("acme")}, {Key: aws.String("stack"), Value: aws.String("prod")}}
	})
	if err != nil {
		t.Fatal(err)
	}
	creds := out.Credentials
	if !strings.HasPrefix(aws.ToString(creds.AccessKeyId), "ASIA") || len(aws.ToString(creds.SecretAccessKey)) != 40 || aws.ToString(creds.SessionToken) == "" {
		t.Fatal("credentials are not shaped as STS's are")
	}
	if left := time.Until(aws.ToTime(creds.Expiration)); left < 29*time.Minute || left > 31*time.Minute {
		t.Fatalf("expires in %v", left)
	}
	if got := aws.ToString(out.AssumedRoleUser.Arn); got != "arn:aws:sts::222222222222:assumed-role/infraweave/infraweave@stack" {
		t.Fatalf("assumed role ARN = %s", got)
	}
	calls := f.Calls()
	want := awsfake.Call{
		Action: "AssumeRole", AccessKeyID: serverKey, RoleARN: roleARN, ExternalID: "acme@01J@prod@r", SessionName: "infraweave@stack",
		Duration: 30 * time.Minute, Region: "eu-west-1", Status: http.StatusOK,
	}
	if len(calls) != 1 || calls[0].Tags["account"] != "acme" || calls[0].Tags["stack"] != "prod" || len(calls[0].Tags) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	got := calls[0]
	got.Tags = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call = %+v, want %+v", got, want)
	}

	// The issued credentials are a caller in turn, as the assumed role.
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	assumed := sts.New(sts.Options{
		Region: "eu-west-1", BaseEndpoint: aws.String(srv.URL), RetryMaxAttempts: 1,
		Credentials: credentials.NewStaticCredentialsProvider(aws.ToString(creds.AccessKeyId), aws.ToString(creds.SecretAccessKey), aws.ToString(creds.SessionToken)),
	})
	id, err := assumed.GetCallerIdentity(t.Context(), &sts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(id.Account) != customerAcct || aws.ToString(id.Arn) != aws.ToString(out.AssumedRoleUser.Arn) {
		t.Fatalf("assumed identity = %s %s", aws.ToString(id.Account), aws.ToString(id.Arn))
	}
}

func TestUnitAssumeRoleDefaults(t *testing.T) {
	f, c := setup(t)
	f.AddRole(awsfake.Role{ARN: "arn:aws:iam::222222222222:role/open", Trusted: []string{"arn:aws:iam::111111111111:root"}})
	out, err := c.AssumeRole(t.Context(), &sts.AssumeRoleInput{RoleArn: aws.String("arn:aws:iam::222222222222:role/open"), RoleSessionName: aws.String("s")})
	if err != nil {
		t.Fatal(err)
	}
	if left := time.Until(aws.ToTime(out.Credentials.Expiration)); left < 59*time.Minute || left > 61*time.Minute {
		t.Fatalf("expires in %v", left)
	}
	if calls := f.Calls(); calls[0].Duration != 0 || calls[0].Tags != nil {
		t.Fatalf("call = %+v", calls[0])
	}
}

func TestUnitAssumeRoleRefused(t *testing.T) {
	f, c := setup(t)
	f.AddRole(awsfake.Role{ARN: "arn:aws:iam::222222222222:role/other", Trusted: []string{"333333333333", "arn:aws:iam::111111111111:user/someone"}})
	f.AddRole(awsfake.Role{ARN: "arn:aws:iam::222222222222:role/byarn", Trusted: []string{serverARN}, MaxDuration: 2 * time.Hour})
	for name, tc := range map[string]struct {
		external, role string
		duration       int32
		code           string
	}{
		"untrusted principal":     {role: "arn:aws:iam::222222222222:role/other", code: "AccessDenied"},
		"unknown role":            {role: "arn:aws:iam::222222222222:role/missing", code: "AccessDenied"},
		"missing external ID":     {code: "AccessDenied"},
		"wrong external ID":       {external: "acme@01J@dev@r", code: "AccessDenied"},
		"wildcard one character":  {external: "acme@01J@prod@rw", code: "AccessDenied"},
		"trusted by ARN":          {role: "arn:aws:iam::222222222222:role/byarn", duration: 7200},
		"wildcard empty run":      {external: "acme@@prod@w"},
		"over the role's maximum": {external: "acme@x@prod@r", duration: 3601, code: "ValidationError"},
		"over STS's maximum":      {role: "arn:aws:iam::222222222222:role/byarn", duration: 43201, code: "ValidationError"},
		"under STS's minimum":     {external: "acme@x@prod@r", duration: 899, code: "ValidationError"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := assume(t.Context(), c, tc.external, func(in *sts.AssumeRoleInput) {
				if tc.role != "" {
					in.RoleArn = aws.String(tc.role)
				}
				if tc.duration != 0 {
					in.DurationSeconds = aws.Int32(tc.duration)
				}
			})
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if code(t, err) != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			if tc.code == "AccessDenied" && !strings.Contains(err.Error(), "User: "+serverARN+" is not authorized to perform: sts:AssumeRole on resource: ") {
				t.Fatalf("message = %v", err)
			}
		})
	}
}

func TestUnitSessionName(t *testing.T) {
	_, c := setup(t)
	for name, session := range map[string]string{
		"empty":     "",
		"too long":  strings.Repeat("a", 65),
		"bad chars": "infra weave",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := assume(t.Context(), c, "acme@x@prod@r", func(in *sts.AssumeRoleInput) { in.RoleSessionName = aws.String(session) })
			if code(t, err) != "ValidationError" {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func post(t *testing.T, base string, form url.Values, auth string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestUnitRawRequests(t *testing.T) {
	f, _ := setup(t)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	auth := "AWS4-HMAC-SHA256 Credential=" + serverKey + "/20260101/us-west-2/sts/aws4_request, SignedHeaders=host, Signature=00"

	status, body := post(t, srv.URL, url.Values{"Action": {"GetCallerIdentity"}, "Version": {"2011-06-15"}}, auth)
	if status != http.StatusOK || !strings.Contains(body, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">`) ||
		!strings.Contains(body, "<Account>"+serverAcct+"</Account>") {
		t.Fatalf("%d %s", status, body)
	}
	status, body = post(t, srv.URL, url.Values{"Action": {"GetCallerIdentity"}}, "")
	if status != http.StatusForbidden || !strings.Contains(body, "<Type>Sender</Type><Code>InvalidClientTokenId</Code>") {
		t.Fatalf("%d %s", status, body)
	}
	status, body = post(t, srv.URL, url.Values{"Action": {"GetSessionToken"}}, auth)
	if status != http.StatusBadRequest || !strings.Contains(body, "<Code>InvalidAction</Code>") {
		t.Fatalf("%d %s", status, body)
	}
	status, body = post(t, srv.URL, url.Values{"Action": {"AssumeRole"}, "RoleSessionName": {"s"}}, auth)
	if status != http.StatusBadRequest || !strings.Contains(body, "roleArn") {
		t.Fatalf("%d %s", status, body)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/?Action=GetCallerIdentity&Version=2011-06-15", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d", resp.StatusCode)
	}

	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/?%zz", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad query = %d", resp.StatusCode)
	}

	calls := f.Calls()
	if len(calls) != 5 || calls[0].Region != "us-west-2" || calls[1].AccessKeyID != "" || calls[2].Code != "InvalidAction" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUnitFaults(t *testing.T) {
	f, c := setup(t)
	for name, tc := range map[string]struct {
		fault  awsfake.Fault
		status int
		code   string
	}{
		"server error": {awsfake.FaultServerError, http.StatusInternalServerError, "InternalFailure"},
		"throttle":     {awsfake.FaultThrottle, http.StatusBadRequest, "Throttling"},
		"malformed":    {awsfake.FaultMalformed, http.StatusOK, ""},
	} {
		t.Run(name, func(t *testing.T) {
			f.Reset()
			f.FailNext(tc.fault, 1)
			_, err := c.GetCallerIdentity(t.Context(), &sts.GetCallerIdentityInput{})
			if err == nil {
				t.Fatal("faulted call succeeded")
			}
			if tc.code != "" && code(t, err) != tc.code {
				t.Fatalf("error = %v", err)
			}
			var de *smithy.DeserializationError
			if tc.code == "" && !errors.As(err, &de) {
				t.Fatalf("error = %v, want a deserialization error", err)
			}
			if _, err := c.GetCallerIdentity(t.Context(), &sts.GetCallerIdentityInput{}); err != nil {
				t.Fatalf("the fault outlived its count: %v", err)
			}
			calls := f.Calls()
			if len(calls) != 2 || calls[0].Status != tc.status || calls[0].Code != tc.code || calls[1].Status != http.StatusOK {
				t.Fatalf("calls = %+v", calls)
			}
		})
	}
}

func TestUnitTimeout(t *testing.T) {
	f, c := setup(t)
	f.FailNext(awsfake.FaultTimeout, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := c.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if calls := f.Calls(); len(calls) != 1 || calls[0].Status != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestUnitFailRole(t *testing.T) {
	f, c := setup(t)
	f.FailRole(roleARN, awsfake.FaultThrottle)
	for range 2 {
		if _, err := assume(t.Context(), c, "acme@x@prod@r"); code(t, err) != "Throttling" {
			t.Fatalf("error = %v", err)
		}
	}
	if _, err := c.GetCallerIdentity(t.Context(), &sts.GetCallerIdentityInput{}); err != nil {
		t.Fatalf("a role's fault reached another action: %v", err)
	}
	f.FailRole(roleARN, awsfake.FaultNone)
	if _, err := assume(t.Context(), c, "acme@x@prod@r"); err != nil {
		t.Fatal(err)
	}
	f.FailRole(roleARN, awsfake.FaultServerError)
	f.Reset()
	if _, err := assume(t.Context(), c, "acme@x@prod@r"); err != nil {
		t.Fatalf("Reset kept a role's fault: %v", err)
	}
	if calls := f.Calls(); len(calls) != 1 {
		t.Fatalf("Reset kept calls: %+v", calls)
	}
}

func TestUnitCallsAreCopies(t *testing.T) {
	f, c := setup(t)
	if _, err := assume(t.Context(), c, "acme@x@prod@r", func(in *sts.AssumeRoleInput) {
		in.Tags = []types.Tag{{Key: aws.String("k"), Value: aws.String("v")}}
	}); err != nil {
		t.Fatal(err)
	}
	f.Calls()[0].Tags["k"] = "changed"
	if f.Calls()[0].Tags["k"] != "v" {
		t.Fatal("Calls shares its tags with the fake")
	}
}
