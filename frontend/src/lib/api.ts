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
  quantity: number
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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...init?.headers },
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
  getMe: () => request<{ hasEmail: boolean; hasPasskey: boolean }>('/me'),
  getMyBills: () => request<SessionSummary[]>('/me/bills'),

  createSession: () => request<SessionSummary>('/sessions', { method: 'POST' }),
  getSession: (id: string) => request<SessionDetail>(`/sessions/${id}`),
  updateSession: (
    id: string,
    patch: Partial<{ title: string; restaurantName: string; billDate: string; totalPaidCents: number }>,
  ) => request<void>(`/sessions/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),

  addPerson: (sessionId: string, name: string) =>
    request<Person>(`/sessions/${sessionId}/people`, { method: 'POST', body: JSON.stringify({ name }) }),
  renamePerson: (personId: string, name: string) =>
    request<void>(`/people/${personId}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  deletePerson: (personId: string) => request<void>(`/people/${personId}`, { method: 'DELETE' }),

  replaceDishes: (
    sessionId: string,
    dishes: { name: string; unitPriceCents: number; quantity: number; source?: string }[],
  ) => request<Dish[]>(`/sessions/${sessionId}/dishes/bulk`, { method: 'POST', body: JSON.stringify({ dishes }) }),
  deleteDish: (dishId: string) => request<void>(`/dishes/${dishId}`, { method: 'DELETE' }),

  upsertPortion: (dishId: string, personId: string, shares: number) =>
    request<void>('/portions', { method: 'PUT', body: JSON.stringify({ dishId, personId, shares }) }),

  getBreakdown: (sessionId: string) =>
    request<{ session: SessionSummary; result: BreakdownResult }>(`/sessions/${sessionId}/breakdown`),

  uploadReceipt: async (sessionId: string, file: File | Blob) => {
    const form = new FormData()
    form.append('receipt', file, 'receipt.jpg')
    const res = await fetch(`/api/sessions/${sessionId}/receipt`, {
      method: 'POST',
      credentials: 'include',
      body: form,
    })
    if (!res.ok) throw new ApiError(res.status, 'upload failed')
  },
  receiptUrl: (sessionId: string) => `/api/sessions/${sessionId}/receipt`,

  extract: (sessionId: string) =>
    request<{ restaurantName?: string; date?: string; items: { name: string; priceCents: number; quantity: number }[] }>(
      `/sessions/${sessionId}/extract`,
      { method: 'POST' },
    ),

  createShare: (sessionId: string) =>
    request<{ viewToken: string; shareUrl: string }>(`/sessions/${sessionId}/share`, { method: 'POST' }),

  getPublicView: (token: string) =>
    request<{
      title: string | null
      restaurantName: string | null
      billDate: string | null
      subtotalCents: number
      totalPaidCents: number | null
      hasReceipt: boolean
      people: { id: string; name: string; sortOrder: number }[]
      result: BreakdownResult
    }>(`/view/${token}`),
  publicReceiptUrl: (token: string) => `/api/view/${token}/receipt`,
}

export { ApiError }
