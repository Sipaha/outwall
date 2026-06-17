import { fieldControlClass } from './FormField'

export interface SelectOption {
  value: string
  label: string
}

interface SelectProps {
  value: string
  onChange: (value: string) => void
  options: SelectOption[]
}

/** A native <select> styled to the dark theme (shares fieldControlClass with inputs). */
export function Select({ value, onChange, options }: SelectProps) {
  return (
    <select className={fieldControlClass} value={value} onChange={(e) => onChange(e.target.value)}>
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  )
}
