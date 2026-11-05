// Package y breaks public-packages: a pkg/ package imports internal/.
package y

import _ "github.com/weaveplatform/infraweave/internal/kernel"
