package http

import (
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

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
