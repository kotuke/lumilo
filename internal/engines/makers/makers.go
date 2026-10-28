// Package makers holds the logos of model makers that engines show next to
// the models they suggest. The logos are the colour icons of Lobe Icons
// (@lobehub/icons-static-svg 1.101.0, MIT), with a fixed 24×24 size and
// without their <title>, as the agent logos of the splash engine.
package makers

import (
	"embed"

	"github.com/kotuke/lumilo/internal/engines/enginekit"
)

//go:embed logos/*.svg
var logos embed.FS

// Qwen is the logo of Qwen (Alibaba Cloud), icons/qwen-color.svg.
var Qwen = enginekit.MustFile(logos, "logos/qwen.svg")
