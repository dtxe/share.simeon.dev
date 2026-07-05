import { create } from 'zustand'

interface AssignSelectionState {
  selectedDishId: string | null
  selectedPersonId: string | null
  selectDish: (id: string) => void
  selectPerson: (id: string) => void
  clearSelection: () => void
}

export const useAssignSelection = create<AssignSelectionState>((set, get) => ({
  selectedDishId: null,
  selectedPersonId: null,
  selectDish: (id) =>
    set((s) => ({
      selectedDishId: s.selectedDishId === id ? null : id,
      selectedPersonId: null,
    })),
  selectPerson: (id) => {
    const { selectedDishId } = get()
    if (selectedDishId) {
      // A dish is already selected — tapping a person in that mode assigns
      // a share, it doesn't switch modes. Callers should check
      // selectedDishId first and only call selectPerson when idle.
      return
    }
    set((s) => ({
      selectedPersonId: s.selectedPersonId === id ? null : id,
      selectedDishId: null,
    }))
  },
  clearSelection: () => set({ selectedDishId: null, selectedPersonId: null }),
}))
