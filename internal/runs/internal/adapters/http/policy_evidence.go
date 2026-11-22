package http

import (
	"context"
	"strings"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

// RunPolicyReceiptSample follows an owned receipt through the policies port.
func (s Server) RunPolicyReceiptSample(ctx context.Context, req api.RunPolicyReceiptSampleRequestObject) (api.RunPolicyReceiptSampleResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := s.read.PolicySample(ctx, p, req.RunId, req.Params.Id)
	if err != nil {
		return nil, runError(err, "", req.RunId)
	}
	out := api.PolicyEvaluationSample{
		Body:           doc.Body,
		EngineType:     api.PolicyEngineType(doc.Engine),
		Input:          string(doc.Input),
		Results:        string(doc.Result),
		Outcome:        strings.ToLower(string(doc.Outcome)),
		Print:          doc.Print,
		PrintTruncated: doc.PrintTruncated,
		Libraries:      []api.SampledPolicyLibrary{},
	}
	if doc.Error != nil {
		out.Error = doc.Error.Message
	}
	return api.RunPolicyReceiptSample200JSONResponse{Body: out}, nil
}

func receiptsOf(receipts []read.PolicyReceipt) []api.RunPolicyReceipt {
	out := make([]api.RunPolicyReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		var slug *string
		if !receipt.Deleted {
			id := receipt.Policy.String()
			slug = &id
		}
		version := receipt.PolicyVersion
		flags := receipt.Flags
		if flags == nil {
			flags = []string{}
		}
		value := api.RunPolicyReceipt{
			Id: receipt.ID.String(), CreatedAt: unix(receipt.CreatedAt.Unix()),
			EngineType: api.PolicyEngineType(receipt.Engine), PolicyType: api.PolicyType(receipt.Type),
			Outcome: string(receipt.Outcome), Flags: flags, PolicyName: receipt.PolicyName,
			PolicyDeleted: receipt.Deleted, PolicySlug: slug, PolicyVersion: &version,
			Sampled: receipt.Sampled, SampleExpired: receipt.SampleExpired,
		}
		if receipt.Failure != nil {
			value.Error = &api.RunPolicyError{Code: receipt.Failure.Code, Message: receipt.Failure.Message}
		}
		out = append(out, value)
	}
	return out
}
