import type {
  AuthenticationResponseJSON,
  PublicKeyCredentialCreationOptionsJSON,
  PublicKeyCredentialRequestOptionsJSON,
  RegistrationResponseJSON,
} from '@simplewebauthn/browser'

export interface SessionSummary {
  id: string
  title: string | null
  restaurantName: string | null
  billDate: string | null
  subtotalCents: number
  totalPaidCents: number | null
  hasReceipt: boolean
  createdAt: string
  updatedAt: string
}

export interface Person {
  id: string
  sessionId: string
  name: string
  sortOrder: number
}

export interface Dish {
  id: string
  sessionId: string
  name: string
  unitPriceCents: number
  sortOrder: number
  source: 'manual' | 'llm_extracted'
}

export interface Portion {
  dishId: string
  personId: string
  shares: number
}

export interface BreakdownResult {
  subtotalCents: number
  people: { personId: string; owedCents: number }[]
  unassignedDishIds: string[] | null
}

export interface SessionDetail {
  session: SessionSummary
  people: Person[]
  dishes: Dish[]
  portions: Portion[]
}

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

// The backend is the real authorization boundary (owner checks on every
// bill route) — this just lets screens recognize a 401/403 from a query and
// show something better than a stuck loading state.
export function isAuthError(err: unknown): boolean {
  return err instanceof ApiError && (err.status === 401 || err.status === 403)
}

// Non-GET requests carry this header so the backend's Origin+header CSRF
// check (relied on since SameSite=Lax cookies alone aren't sufficient) can
// tell a same-origin fetch/XHR apart from a cross-site HTML form post —
// forms can't set custom headers, but this can't be spoofed cross-origin
// without triggering a CORS preflight that our origin allowlist blocks.
const CSRF_HEADERS = { 'X-Requested-With': 'XMLHttpRequest' }

// Dynamic path segments (session/person/dish ids, share tokens) come from
// route params or server responses — encode them so a value containing
// `/`, `?`, `#`, etc. can't reshape the request path.
function enc(segment: string): string {
  return encodeURIComponent(segment)
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? 'GET').toUpperCase()
  const csrfHeaders = method === 'GET' || method === 'HEAD' ? {} : CSRF_HEADERS
  const res = await fetch(`/api${path}`, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...csrfHeaders, ...init?.headers },
    ...init,
  })
  if (!res.ok) {
    let message = `request failed (${res.status})`
    try {
      const body = await res.json()
      if (body?.error) message = body.error
    } catch {
      // ignore non-JSON error bodies
    }
    throw new ApiError(res.status, message)
  }
  if (res.status === 204 || res.headers.get('content-length') === '0') {
    return undefined as T
  }
  return res.json() as Promise<T>
}

export const api = {
  getMe: () => request<{ email: string | null; hasEmail: boolean; hasPasskey: boolean; passkeysEnabled: boolean }>('/me'),
  getMyBills: () => request<SessionSummary[]>('/me/bills'),

  beginPasskeyRegistration: () =>
    request<{ ceremonyId: string; options: PublicKeyCredentialCreationOptionsJSON }>('/auth/passkey/register/options', {
      method: 'POST',
    }),
  finishPasskeyRegistration: (ceremonyId: string, response: RegistrationResponseJSON) =>
    request<void>(`/auth/passkey/register/verify?ceremonyId=${encodeURIComponent(ceremonyId)}`, {
      method: 'POST',
      body: JSON.stringify(response),
    }),
  beginPasskeyLogin: () =>
    request<{ ceremonyId: string; options: PublicKeyCredentialRequestOptionsJSON }>('/auth/passkey/login/options', {
      method: 'POST',
    }),
  finishPasskeyLogin: (ceremonyId: string, response: AuthenticationResponseJSON) =>
    request<void>(`/auth/passkey/login/verify?ceremonyId=${encodeURIComponent(ceremonyId)}`, {
      method: 'POST',
      body: JSON.stringify(response),
    }),

  createSession: () => request<SessionSummary>('/sessions', { method: 'POST' }),
  getSession: (id: string) => request<SessionDetail>(`/sessions/${enc(id)}`),
  updateSession: (
    id: string,
    patch: Partial<{ title: string; restaurantName: string; billDate: string; totalPaidCents: number }>,
  ) => request<void>(`/sessions/${enc(id)}`, { method: 'PATCH', body: JSON.stringify(patch) }),

  addPerson: (sessionId: string, name: string) =>
    request<Person>(`/sessions/${enc(sessionId)}/people`, { method: 'POST', body: JSON.stringify({ name }) }),
  renamePerson: (personId: string, name: string) =>
    request<void>(`/people/${enc(personId)}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  deletePerson: (personId: string) => request<void>(`/people/${enc(personId)}`, { method: 'DELETE' }),

  replaceDishes: (
    sessionId: string,
    dishes: { name: string; unitPriceCents: number; source?: string }[],
  ) => request<Dish[]>(`/sessions/${enc(sessionId)}/dishes/bulk`, { method: 'POST', body: JSON.stringify({ dishes }) }),
  addDish: (sessionId: string, dish: { name: string; unitPriceCents: number }) =>
    request<Dish>(`/sessions/${enc(sessionId)}/dishes`, { method: 'POST', body: JSON.stringify(dish) }),
  updateDish: (dishId: string, patch: Partial<{ name: string; unitPriceCents: number }>) =>
    request<void>(`/dishes/${enc(dishId)}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  deleteDish: (dishId: string) => request<void>(`/dishes/${enc(dishId)}`, { method: 'DELETE' }),

  upsertPortion: (dishId: string, personId: string, shares: number) =>
    request<void>('/portions', { method: 'PUT', body: JSON.stringify({ dishId, personId, shares }) }),

  getBreakdown: (sessionId: string) =>
    request<{ session: SessionSummary; result: BreakdownResult }>(`/sessions/${enc(sessionId)}/breakdown`),

  uploadReceipt: async (sessionId: string, file: File | Blob) => {
    const form = new FormData()
    form.append('receipt', file, 'receipt.jpg')
    const res = await fetch(`/api/sessions/${enc(sessionId)}/receipt`, {
      method: 'POST',
      credentials: 'include',
      headers: CSRF_HEADERS,
      body: form,
    })
    if (!res.ok) {
      let message = ''
      try {
        const body = await res.json()
        if (body?.error) message = body.error
      } catch {
        // ignore non-JSON error bodies
      }
      throw new ApiError(res.status, message || `Upload failed (HTTP ${res.status}).`)
    }
  },
  receiptUrl: (sessionId: string) => `/api/sessions/${enc(sessionId)}/receipt`,

  requestOtp: (email: string) => request<void>('/auth/otp/request', { method: 'POST', body: JSON.stringify({ email }) }),
  verifyOtp: (email: string, code: string) =>
    request<void>('/auth/otp/verify', { method: 'POST', body: JSON.stringify({ email, code }) }),
  logout: () => request<void>('/auth/logout', { method: 'POST' }),

  extract: (sessionId: string) =>
    request<{
      restaurantName?: string
      date?: string
      subtotalCents?: number
      tipCents?: number
      totalPaidCents?: number
      items: { name: string; priceCents: number }[]
    }>(`/sessions/${enc(sessionId)}/extract`, { method: 'POST' }),

  createShare: (sessionId: string) =>
    request<{ viewToken: string; shareUrl: string }>(`/sessions/${enc(sessionId)}/share`, { method: 'POST' }),

  getPublicView: (token: string) =>
    request<{
      title: string | null
      restaurantName: string | null
      billDate: string | null
      subtotalCents: number
      totalPaidCents: number | null
      hasReceipt: boolean
      people: { id: string; name: string; sortOrder: number }[]
      dishes?: { id: string; name: string; unitPriceCents: number }[]
      portions?: { dishId: string; personId: string; shares: number }[]
      result: BreakdownResult
    }>(`/view/${enc(token)}`),
  publicReceiptUrl: (token: string) => `/api/view/${enc(token)}/receipt`,
}

export { ApiError }
