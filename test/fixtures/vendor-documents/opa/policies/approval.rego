package spacelift

approve if count(input.reviews.current.approvals) > 0

reject if count(input.reviews.current.rejections) > 0

sample := true
