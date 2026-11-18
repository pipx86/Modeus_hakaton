/**
 * @file utils.ts
 * @description Утилита cn: склейка tailwind-классов с разрешением конфликтов.
 */

import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}