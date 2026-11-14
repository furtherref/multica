export interface OfficeConfig {
  /** Public Document Server base URL — the frontend loads api.js from here. */
  document_server_url: string;
  /**
   * The OnlyOffice DocEditor config, already JWT-signed by the backend
   * (`config.token`). Opaque to the client — passed straight to DocsAPI.
   */
  config: Record<string, unknown>;
}
