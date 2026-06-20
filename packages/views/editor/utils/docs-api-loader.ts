// Minimal typing for the OnlyOffice browser API. We use only the DocEditor
// constructor and its destroyEditor lifecycle method.
export interface DocsApiEditor {
  destroyEditor(): void;
}
export interface DocsApi {
  DocEditor: new (
    placeholderId: string,
    config: Record<string, unknown>,
  ) => DocsApiEditor;
}

declare global {
  interface Window {
    DocsAPI?: DocsApi;
  }
}

// One in-flight promise per documentServerUrl so concurrent previews share a
// single <script> injection. Resolves when window.DocsAPI is available.
const loaders = new Map<string, Promise<DocsApi>>();

export function loadDocsApi(documentServerUrl: string): Promise<DocsApi> {
  if (typeof window !== "undefined" && window.DocsAPI) {
    return Promise.resolve(window.DocsAPI);
  }
  const cached = loaders.get(documentServerUrl);
  if (cached) return cached;

  const src = `${documentServerUrl.replace(/\/$/, "")}/web-apps/apps/api/documents/api.js`;
  const promise = new Promise<DocsApi>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = src;
    script.async = true;
    script.onload = () => {
      if (window.DocsAPI) {
        resolve(window.DocsAPI);
      } else {
        // Script loaded (200) but the global never appeared (broken/partial
        // bundle). Clear the cache like the onerror path so reopening retries.
        loaders.delete(documentServerUrl);
        reject(new Error("DocsAPI not available after api.js load"));
      }
    };
    script.onerror = () => {
      loaders.delete(documentServerUrl);
      reject(new Error(`failed to load OnlyOffice api.js from ${src}`));
    };
    document.head.appendChild(script);
  });
  loaders.set(documentServerUrl, promise);
  return promise;
}

// Test seam: reset the module-level cache between tests.
export function __resetDocsApiLoaderForTest(): void {
  loaders.clear();
}
