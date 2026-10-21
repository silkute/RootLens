let csrf = "";
export function setCSRF(value: string) {
  csrf = value;
}
export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 15000);
  try {
    const response = await fetch(`/api/v1${path}`, {
      ...options,
      signal: controller.signal,
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": csrf,
        ...options.headers,
      },
    });
    if (!response.ok) {
      const data = await response.json().catch(() => ({}));
      throw new APIError(
        response.status,
        data.code || "request_failed",
        data.message || "This request could not be completed.",
      );
    }
    return response.status === 204 ? (undefined as T) : await response.json();
  } finally {
    clearTimeout(timer);
  }
}
export function mutate<T>(path: string, method: string, data?: unknown) {
  return api<T>(path, {
    method,
    body: data === undefined ? undefined : JSON.stringify(data),
  });
}
export function number(value?: number, unit = ""): string {
  if (value === undefined || !Number.isFinite(value)) return "—";
  if (unit === "bytes" || unit === "rate") {
    const suffix = unit === "rate" ? "/s" : "";
    for (const [size, label] of [
      [1099511627776, "TiB"],
      [1073741824, "GiB"],
      [1048576, "MiB"],
      [1024, "KiB"],
    ] as const)
      if (value >= size)
        return `${(value / size).toFixed(1)} ${label}${suffix}`;
    return `${value.toFixed(0)} B${suffix}`;
  }
  return `${value.toFixed(unit === "%" ? 1 : 2)}${unit}`;
}
export function relative(at?: string | null): string {
  if (!at) return "Waiting for data";
  const seconds = Math.max(0, (Date.now() - Date.parse(at)) / 1000);
  if (seconds < 10) return "Just now";
  if (seconds < 60) return `${Math.floor(seconds)}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}
export function clock(at: string) {
  return new Date(at).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}
export function date(at: string) {
  return new Date(at).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
