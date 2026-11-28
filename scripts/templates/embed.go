package templates

import _ "embed"

//go:embed comfyui-deploy.sh
var ComfyUIDeployScript string

//go:embed llm-proxy.sh
var LLMProxyScript string
