package server

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed openapi/gateway.openapi.yaml
var gatewayOpenAPIYAML []byte

//go:embed openapi/swagger-ui-bundle.js
var swaggerUIBundleJS []byte

//go:embed openapi/swagger-ui.css
var swaggerUICSS []byte

//go:embed openapi/swagger-ui-bundle.js.LICENSE.txt
var swaggerUIBundleLicense []byte

//go:embed openapi/swagger-ui-dist.LICENSE
var swaggerUIDistLicense []byte

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/docs" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Documentation page not found"))
		return
	}
	if r.Method != http.MethodGet {
		jsonMethodNotAllowed(http.MethodGet)(w, r)
		return
	}
	origin, err := s.openapiServerURL(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := gatewayDocsTemplate.Execute(w, gatewayDocsTemplateData{
		OpenAPIURL:           publicBasePathURL(origin, "/openapi.json"),
		OpenAPIURLJSON:       mustJSONString(publicBasePathURL(origin, "/openapi.json")),
		ServerURLJSON:        mustJSONString(origin),
		SwaggerCSSURL:        publicBasePathURL(origin, "/docs/swagger-ui.css"),
		SwaggerBundleURL:     publicBasePathURL(origin, "/docs/swagger-ui-bundle.js"),
		SwaggerBundleLicense: publicBasePathURL(origin, "/docs/swagger-ui-bundle.js.LICENSE.txt"),
		SwaggerDistLicense:   publicBasePathURL(origin, "/docs/swagger-ui-dist.LICENSE"),
	}); err != nil {
		logTemplateError("OpenAPI documentation", err)
	}
}

func (s *Server) handleDocsSwaggerUICSS(w http.ResponseWriter, r *http.Request) {
	serveOpenAPIAsset(w, "text/css; charset=utf-8", swaggerUICSS)
}

func (s *Server) handleDocsSwaggerUIBundle(w http.ResponseWriter, r *http.Request) {
	serveOpenAPIAsset(w, "application/javascript; charset=utf-8", swaggerUIBundleJS)
}

func (s *Server) handleDocsSwaggerUIBundleLicense(w http.ResponseWriter, r *http.Request) {
	serveOpenAPIAsset(w, "text/plain; charset=utf-8", swaggerUIBundleLicense)
}

func (s *Server) handleDocsSwaggerUIDistLicense(w http.ResponseWriter, r *http.Request) {
	serveOpenAPIAsset(w, "text/plain; charset=utf-8", swaggerUIDistLicense)
}

func serveOpenAPIAsset(w http.ResponseWriter, contentType string, payload []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(payload)
}

func (s *Server) handleOpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonMethodNotAllowed(http.MethodGet)(w, r)
		return
	}
	payload, err := s.openapiPayload(r, true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(payload)
}

func (s *Server) handleOpenAPIYAML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonMethodNotAllowed(http.MethodGet)(w, r)
		return
	}
	payload, err := s.openapiPayload(r, false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(payload)
}

func (s *Server) openapiPayload(r *http.Request, asJSON bool) ([]byte, error) {
	origin, err := s.openapiServerURL(r)
	if err != nil {
		return nil, err
	}
	document, err := gatewayOpenAPIDocument(origin, s.config.AppVersion)
	if err != nil {
		return nil, NewHTTPError(http.StatusInternalServerError, "openapi_document_invalid", "OpenAPI document is invalid")
	}
	if asJSON {
		return json.MarshalIndent(document, "", "  ")
	}
	return yaml.Marshal(document)
}

func gatewayOpenAPIDocument(serverURL, appVersion string) (map[string]any, error) {
	var document map[string]any
	if err := yaml.Unmarshal(gatewayOpenAPIYAML, &document); err != nil {
		return nil, err
	}
	document["servers"] = []any{map[string]any{
		"url":         strings.TrimRight(serverURL, "/"),
		"description": "TokenHub gateway origin for this deployment.",
	}}
	if info, ok := document["info"].(map[string]any); ok {
		if version := strings.TrimSpace(appVersion); version != "" {
			info["version"] = version
		}
	}
	return document, nil
}

func (s *Server) openapiServerURL(r *http.Request) (string, error) {
	if configured := strings.TrimSpace(s.config.PublicBaseURL); configured != "" {
		return normalizeOpenAPIServerURL(configured, true)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if ipMatchesTrustedProxy(requestRemoteIP(r), s.config.TrustedProxyCIDRs) {
		if forwarded := firstForwardedValue(r.Header.Get("x-forwarded-proto")); forwarded != "" {
			scheme = forwarded
		}
		if forwarded := firstForwardedValue(r.Header.Get("x-forwarded-host")); forwarded != "" {
			host = forwarded
		}
	}
	return normalizeOpenAPIServerURL(fmt.Sprintf("%s://%s", scheme, host), true)
}

func normalizeOpenAPIServerURL(raw string, allowHTTP bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return "", NewHTTPError(http.StatusInternalServerError, "invalid_public_base_url", "Public base URL is invalid")
	}
	if parsed.Fragment != "" {
		return "", NewHTTPError(http.StatusInternalServerError, "invalid_public_base_url", "Public base URL must not include a fragment")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		if !allowHTTP && !isOAuthLoopbackHost(parsed.Hostname()) {
			return "", NewHTTPError(http.StatusInternalServerError, "invalid_public_base_url", "Public base URL must use https unless it is loopback")
		}
	default:
		return "", NewHTTPError(http.StatusInternalServerError, "invalid_public_base_url", "Public base URL must use http or https")
	}
	parsed.RawQuery = ""
	parsed.Path = strings.TrimRight(parsed.EscapedPath(), "/")
	if parsed.Path == "." {
		parsed.Path = ""
	}
	return parsed.String(), nil
}

func publicBasePathURL(serverURL string, suffix string) string {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return suffix
	}
	basePath := strings.TrimRight(parsed.EscapedPath(), "/")
	if basePath == "." {
		basePath = ""
	}
	return basePath + suffix
}

func logTemplateError(name string, err error) {
	if err != nil {
		log.Printf("[tokenhub] failed to render %s: %v", name, err)
	}
}

var gatewayDocsTemplate = template.Must(template.New("gateway-docs").Parse(gatewayDocsHTML))

type gatewayDocsTemplateData struct {
	OpenAPIURL           string
	OpenAPIURLJSON       template.JS
	ServerURLJSON        template.JS
	SwaggerCSSURL        string
	SwaggerBundleURL     string
	SwaggerBundleLicense string
	SwaggerDistLicense   string
}

func mustJSONString(value string) template.JS {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return template.JS(payload)
}

const gatewayDocsHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>TokenHub API Reference</title>
  <link rel="stylesheet" href="{{.SwaggerCSSURL}}">
  <style>
    body { margin: 0; background: #f7f8fa; }
    .tokenhub-docs-banner { background: #0f172a; color: #f8fafc; padding: 16px clamp(16px, 4vw, 40px); }
    .tokenhub-docs-banner h1 { margin: 0 0 4px; font: 700 24px/1.2 ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; letter-spacing: 0; }
    .tokenhub-docs-banner p { margin: 0; max-width: 980px; color: #cbd5e1; font: 14px/1.5 ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    .tokenhub-docs-banner a { color: #67e8f9; }
    .swagger-ui .topbar { display: none; }
  </style>
</head>
<body>
  <header class="tokenhub-docs-banner">
    <h1>TokenHub API Reference</h1>
    <p>Self-hosted Swagger UI for the public gateway OpenAPI 3.1 contract. Project API keys entered through Authorize are kept in memory only; persistence is disabled. Browser execution is blocked for multipart, binary, and unsafe operations. <a href="{{.OpenAPIURL}}" target="_blank" rel="noreferrer">Open JSON</a>.</p>
  </header>
  <main id="swagger-ui" aria-label="TokenHub Swagger UI"></main>
  <script src="{{.SwaggerBundleURL}}"></script>
  <script>
    const openAPIURL = {{.OpenAPIURLJSON}};
    const configuredServerURL = {{.ServerURLJSON}};
    let tokenhubSpec = null;

    function normalizedURL(value) {
      const url = new URL(value || window.location.origin, window.location.href);
      url.hash = "";
      url.search = "";
      return url.href.replace(/\/$/, "");
    }

    function tokenHubOperationForRequest(request) {
      if (!tokenhubSpec) return null;
      const requestURL = new URL(request.url, window.location.href);
      const server = new URL(tokenhubSpec.servers?.[0]?.url || configuredServerURL || window.location.origin, window.location.href);
      if (requestURL.origin !== server.origin) return null;
      const basePath = server.pathname.replace(/\/$/, "");
      let path = requestURL.pathname;
      if (basePath && path.startsWith(basePath + "/")) path = path.slice(basePath.length);
      const method = String(request.method || "get").toLowerCase();
      for (const [template, item] of Object.entries(tokenhubSpec.paths || {})) {
        const pattern = new RegExp("^" + template.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/\\\{[^/]+\\\}/g, "[^/]+") + "$");
        if (pattern.test(path) && item[method]) return item[method];
      }
      return null;
    }

    function tokenHubRequestInterceptor(request) {
      const target = new URL(request.url, window.location.href);
      const documented = normalizedURL(tokenhubSpec?.servers?.[0]?.url || configuredServerURL);
      const documentedURL = new URL(documented);
      if (target.origin !== documentedURL.origin) {
        throw new Error("Try it requests are limited to the documented TokenHub origin.");
      }
      const operation = tokenHubOperationForRequest(request);
      if (operation?.["x-tokenhub-interactive"] === false) {
        throw new Error("Browser execution is disabled for this operation by the TokenHub OpenAPI contract.");
      }
      request.credentials = "omit";
      return request;
    }

    function tokenHubTryItPolicyPlugin() {
      return {
        statePlugins: {
          spec: {
            wrapSelectors: {
              allowTryItOutFor: (original) => (state, path, method) => {
                const operation = state.getIn(["spec", "json", "paths", path, method]);
                if (operation && operation.get("x-tokenhub-interactive") === false) return false;
                return original(state, path, method);
              }
            }
          }
        }
      };
    }

    function hardenSwaggerAuthInputs() {
      document.querySelectorAll(".auth-container input, input[aria-label='auth-bearer-value']").forEach((input) => {
        if (!(input instanceof HTMLInputElement)) return;
        input.type = "password";
        input.autocomplete = "off";
        input.setAttribute("data-tokenhub-secret", "true");
      });
    }

    const tokenhubSecretHeaderPattern = /\b(Authorization|x-api-key|x-goog-api-key)(\s*:\s*)(Bearer\s+)?[^\s'"\\]+/gi;

    function redactSwaggerSecretText(value) {
      return value.replace(tokenhubSecretHeaderPattern, (_match, name, separator, prefix) => {
        return name + separator + (prefix || "") + "<redacted>";
      });
    }

    function redactSwaggerExecutionSecrets(root) {
      const scope = root instanceof Element ? root : document.body;
      scope.querySelectorAll("pre, code, .curl-command, .request-url, .request-headers, .microlight").forEach((node) => {
        tokenhubSecretHeaderPattern.lastIndex = 0;
        if (!node.textContent || !tokenhubSecretHeaderPattern.test(node.textContent)) return;
        tokenhubSecretHeaderPattern.lastIndex = 0;
        node.textContent = redactSwaggerSecretText(node.textContent);
      });
      tokenhubSecretHeaderPattern.lastIndex = 0;
    }

    const authInputObserver = new MutationObserver((mutations) => {
      hardenSwaggerAuthInputs();
      for (const mutation of mutations) {
        mutation.addedNodes.forEach((node) => {
          if (node instanceof Element) redactSwaggerExecutionSecrets(node);
        });
        if (mutation.type === "characterData" && mutation.target.parentElement) {
          redactSwaggerExecutionSecrets(mutation.target.parentElement);
        }
      }
    });
    authInputObserver.observe(document.body, { childList: true, characterData: true, subtree: true });

    fetch(openAPIURL, { credentials: "same-origin" })
      .then((response) => response.json())
      .then((spec) => {
        tokenhubSpec = spec;
        SwaggerUIBundle({
          spec,
          dom_id: "#swagger-ui",
          deepLinking: true,
          displayOperationId: true,
          displayRequestDuration: true,
          docExpansion: "list",
          persistAuthorization: false,
          requestSnippetsEnabled: false,
          showMutatedRequest: false,
          tryItOutEnabled: false,
          validatorUrl: null,
          supportedSubmitMethods: ["get", "post"],
          plugins: [tokenHubTryItPolicyPlugin],
          requestInterceptor: tokenHubRequestInterceptor,
          onComplete: () => {
            hardenSwaggerAuthInputs();
            redactSwaggerExecutionSecrets(document.body);
          },
          tagsSorter: "alpha",
          operationsSorter: "alpha"
        });
      })
      .catch((err) => {
        document.getElementById("swagger-ui").textContent = "Unable to load OpenAPI document: " + err.message;
      });
  </script>
  <!-- Swagger UI license notices: {{.SwaggerBundleLicense}} and {{.SwaggerDistLicense}} -->
</body>
</html>`
