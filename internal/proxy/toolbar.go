package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const (
	// HeaderToolbarOptOut is the request header a client can send ("off") to skip
	// automatic HTML injection of the floating DevToolbar.
	HeaderToolbarOptOut = "X-Diverge-Toolbar"

	// maxToolbarInspectBytes caps the maximum HTML response body size (4 MiB) buffered
	// when inspecting for </body> injection.
	maxToolbarInspectBytes = 4 << 20
)

// ToolbarContextResponse is the JSON payload returned by GET /-/api/context.
type ToolbarContextResponse struct {
	ActiveEnvironment string            `json:"activeEnvironment"`
	HeaderKey         string            `json:"headerKey"`
	PreviewDomain     string            `json:"previewDomain"`
	Environments      []EnvironmentInfo `json:"environments,omitempty"`
}

func (s *Server) resolveRequestEnvName(r *http.Request) string {
	host := r.Host
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}

	envName := extractEnvName(host, s.config.PreviewDomain)
	if envName == "" {
		if c, err := r.Cookie(PreviewCookieName); err == nil && isValidPreviewEnvName(c.Value) {
			envName = c.Value
		}
	}
	if envName == "" {
		headerKey := s.config.HeaderKey
		if headerKey == "" {
			headerKey = "x-diverge-env"
		}
		if hdr := r.Header.Get(headerKey); isValidPreviewEnvName(hdr) {
			envName = hdr
		}
	}
	return envName
}

func (s *Server) serveToolbarScript(w http.ResponseWriter, r *http.Request) {
	serveToolbarScript(w, r)
}

func serveToolbarScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(floatingToolbarJS))
}

func (s *Server) serveToolbarContext(w http.ResponseWriter, r *http.Request) {
	headerKey := s.config.HeaderKey
	if headerKey == "" {
		headerKey = "x-diverge-env"
	}

	resp := ToolbarContextResponse{
		ActiveEnvironment: s.resolveRequestEnvName(r),
		HeaderKey:         headerKey,
		PreviewDomain:     s.config.PreviewDomain,
	}

	if !s.config.HideEnvironmentList && s.envLister != nil {
		if envs, err := s.envLister.ListEnvironments(r.Context()); err == nil {
			sorted := make([]EnvironmentInfo, len(envs))
			copy(sorted, envs)
			sort.Slice(sorted, func(i, j int) bool {
				return sorted[i].Name < sorted[j].Name
			})
			resp.Environments = sorted
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) injectToolbarModifyResponse(resp *http.Response) error {
	if s.config.DisableToolbar {
		return nil
	}
	return injectToolbarModifyResponse(resp)
}

func injectToolbarModifyResponse(resp *http.Response) error {
	if resp == nil || resp.Body == nil {
		return nil
	}
	if resp.Request != nil && strings.EqualFold(strings.TrimSpace(resp.Request.Header.Get(HeaderToolbarOptOut)), "off") {
		return nil
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return nil
	}
	if resp.Header.Get("Content-Encoding") != "" {
		return nil
	}

	origBody := resp.Body
	limited := io.LimitReader(origBody, maxToolbarInspectBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		_ = origBody.Close()
		return err
	}

	if len(buf) > maxToolbarInspectBytes {
		resp.Body = &combinedReadCloser{
			Reader: io.MultiReader(bytes.NewReader(buf), origBody),
			Closer: origBody,
		}
		return nil
	}
	_ = origBody.Close()

	bodyStr := string(buf)
	if strings.Contains(bodyStr, "/-/toolbar.js") {
		resp.Body = io.NopCloser(bytes.NewReader(buf))
		return nil
	}

	idx := strings.LastIndex(strings.ToLower(bodyStr), "</body>")
	if idx == -1 {
		resp.Body = io.NopCloser(bytes.NewReader(buf))
		return nil
	}

	modified := bodyStr[:idx] + `<script src="/-/toolbar.js" defer></script>` + bodyStr[idx:]
	modifiedBytes := []byte(modified)

	resp.Body = io.NopCloser(bytes.NewReader(modifiedBytes))
	resp.ContentLength = int64(len(modifiedBytes))
	resp.Header.Set("Content-Length", strconv.Itoa(len(modifiedBytes)))
	return nil
}

type combinedReadCloser struct {
	io.Reader
	io.Closer
}

// editorconfig-checker-disable
const floatingToolbarJS = `(function() {
  if (typeof window === 'undefined' || typeof customElements === 'undefined') return;
  if (customElements.get('diverge-toolbar')) return;

  class DivergeToolbar extends HTMLElement {
    constructor() {
      super();
      this.attachShadow({ mode: 'open' });
      let initialCollapsed = false;
      try {
        initialCollapsed = localStorage.getItem('diverge_toolbar_collapsed') === '1';
      } catch (_) {}
      this.state = {
        activeEnvironment: '',
        headerKey: 'x-diverge-env',
        previewDomain: '',
        environments: [],
        collapsed: initialCollapsed,
        copied: false
      };
    }

    connectedCallback() {
      this.render();
      this.fetchContext();
    }

    toggleCollapse() {
      this.state.collapsed = !this.state.collapsed;
      try {
        localStorage.setItem('diverge_toolbar_collapsed', this.state.collapsed ? '1' : '0');
      } catch (_) {}
      this.render();
    }

    async fetchContext() {
      try {
        const res = await fetch('/-/api/context', { credentials: 'same-origin' });
        if (!res.ok) return;
        const data = await res.json();
        this.state.activeEnvironment = data.activeEnvironment || '';
        this.state.headerKey = data.headerKey || 'x-diverge-env';
        this.state.previewDomain = data.previewDomain || '';
        this.state.environments = Array.isArray(data.environments) ? data.environments : [];
        this.render();
      } catch (_) {}
    }

    buildMagicLink(envName) {
      const target = envName || this.state.activeEnvironment || '';
      const url = new URL(window.location.href);
      if (target) {
        url.searchParams.set('_diverge_env', target);
      }
      return url.toString();
    }

    buildResetLink() {
      const url = new URL(window.location.href);
      url.searchParams.set('_diverge_env', 'clear');
      return url.toString();
    }

    render() {
      const active = this.state.activeEnvironment || 'baseline';
      const envs = this.state.environments || [];
      const optionsHtml = envs.map((e) => {
        const name = e.name || e.Name || '';
        const phase = e.phase || e.Phase || '';
        const selected = name === active ? 'selected' : '';
        return '<option value="' + name + '" ' + selected + '>' + name + (phase ? ' (' + phase + ')' : '') + '</option>';
      }).join('');

      this.shadowRoot.innerHTML = ` + "`" + `
        <style>
          :host {
            position: fixed;
            bottom: 16px;
            right: 16px;
            z-index: 2147483647;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            font-size: 12px;
            color: #f8fafc;
          }
          .toolbar {
            display: flex;
            align-items: center;
            gap: 8px;
            background: rgba(15, 23, 42, 0.94);
            border: 1px solid rgba(139, 92, 246, 0.5);
            border-radius: 9999px;
            padding: 6px 12px;
            box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.5);
            backdrop-filter: blur(8px);
          }
          .toolbar.collapsed {
            padding: 4px 10px;
            gap: 6px;
          }
          .toolbar-controls {
            display: flex;
            align-items: center;
            gap: 8px;
          }
          .toolbar.collapsed .toolbar-controls {
            display: none;
          }
          .badge {
            font-weight: 600;
            white-space: nowrap;
            color: #e2e8f0;
          }
          select, button {
            background: rgba(30, 41, 59, 0.9);
            color: #f8fafc;
            border: 1px solid rgba(148, 163, 184, 0.35);
            border-radius: 9999px;
            padding: 4px 10px;
            font-size: 11px;
            cursor: pointer;
          }
          select:hover, button:hover {
            border-color: rgba(167, 139, 250, 0.8);
          }
        </style>
        <div class="toolbar${this.state.collapsed ? ' collapsed' : ''}" role="region" aria-label="Diverge Preview Toolbar">
          <span class="badge">${this.state.collapsed ? '🔀 ' + active : '🟣 Diverge: ' + active}</span>
          <div class="toolbar-controls" style="${this.state.collapsed ? 'display: none;' : ''}">
            ${envs.length > 0 ? '<select id="env-select" aria-label="Switch preview environment">' + optionsHtml + '</select>' : ''}
            <button id="copy-magic" type="button" title="Copy Magic Link (?_diverge_env=...)">
              ${this.state.copied ? 'Copied!' : 'Copy Magic Link (?_diverge_env=...)'}
            </button>
            <button id="reset-baseline" type="button" title="Reset to Baseline (?_diverge_env=clear)">
              Reset to Baseline (?_diverge_env=clear)
            </button>
          </div>
          <button id="diverge-collapse-btn" type="button" aria-expanded="${!this.state.collapsed}" aria-label="${this.state.collapsed ? 'Expand Diverge toolbar' : 'Collapse Diverge toolbar'}" title="Toggle toolbar">
            ${this.state.collapsed ? '+' : '−'}
          </button>
        </div>
      ` + "`" + `;

      const selectEl = this.shadowRoot.getElementById('env-select');
      if (selectEl) {
        selectEl.addEventListener('change', (ev) => {
          const nextEnv = ev.target.value;
          if (nextEnv) {
            window.location.href = this.buildMagicLink(nextEnv);
          }
        });
      }

      const copyBtn = this.shadowRoot.getElementById('copy-magic');
      if (copyBtn) {
        copyBtn.addEventListener('click', async () => {
          const link = this.buildMagicLink(this.state.activeEnvironment);
          if (navigator.clipboard && navigator.clipboard.writeText) {
            await navigator.clipboard.writeText(link);
            this.state.copied = true;
            this.render();
            setTimeout(() => {
              this.state.copied = false;
              this.render();
            }, 1500);
          }
        });
      }

      const resetBtn = this.shadowRoot.getElementById('reset-baseline');
      if (resetBtn) {
        resetBtn.addEventListener('click', () => {
          window.location.href = this.buildResetLink();
        });
      }

      const collapseBtn = this.shadowRoot.getElementById('diverge-collapse-btn');
      if (collapseBtn) {
        collapseBtn.addEventListener('click', () => this.toggleCollapse());
      }
    }
  }

  customElements.define('diverge-toolbar', DivergeToolbar);

  function mountToolbar() {
    if (!document.querySelector('diverge-toolbar') && document.body) {
      document.body.appendChild(document.createElement('diverge-toolbar'));
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', mountToolbar);
  } else {
    mountToolbar();
  }
})();
`

// editorconfig-checker-enable
