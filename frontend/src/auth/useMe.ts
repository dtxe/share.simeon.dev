import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

export function useMe() {
  return useQuery({ queryKey: ['me'], queryFn: api.getMe })
}

export function useInvalidateMe() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: ['me'] })
}
