import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  children: ReactNode;
  title: string;
  message: string;
  retryLabel: string;
}

interface State {
  failed: boolean;
}

// A render error must leave the operator a usable recovery path instead of a
// blank document. Reloading is deliberate: it also lets a newly activated
// service worker and freshly deployed assets take over.
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { failed: false };

  static getDerivedStateFromError(): State {
    return { failed: true };
  }

  componentDidCatch(_error: Error, _info: ErrorInfo): void {
    // The hub has no browser telemetry endpoint. Keep the boundary local until
    // a privacy-preserving diagnostics route is introduced.
  }

  render(): ReactNode {
    if (this.state.failed) {
      return (
        <main className="auth-shell">
          <section className="auth-panel error-recovery-panel" role="alert">
            <h1>{this.props.title}</h1>
            <p>{this.props.message}</p>
            <button type="button" className="primary-button" onClick={() => window.location.reload()}>
              {this.props.retryLabel}
            </button>
          </section>
        </main>
      );
    }

    return this.props.children;
  }
}
