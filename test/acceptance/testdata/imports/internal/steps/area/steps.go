// A step file that reaches past the SDK, for TestUnitStepsUseTheSDK.
package steps

import (
	"fmt"

	"github.com/weaveplatform/infraweave/pkg/client/api"
)

var _ = fmt.Sprint(api.Stack{})
