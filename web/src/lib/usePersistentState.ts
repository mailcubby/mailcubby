import { useCallback, useEffect, useState } from "react";

export const STORAGE_PREFIX = "mailcubby:";

function toStorageKey(key: string): string {
  return key.startsWith(STORAGE_PREFIX) ? key : `${STORAGE_PREFIX}${key}`;
}

export function readPersistentValue<T>(
  key: string,
  defaultValue: T,
  validate?: (value: unknown) => value is T,
): T {
  if (typeof window === "undefined") {
    return defaultValue;
  }
  try {
    const raw = window.localStorage.getItem(toStorageKey(key));
    if (raw === null) {
      return defaultValue;
    }
    const parsed: unknown = JSON.parse(raw);
    if (validate && !validate(parsed)) {
      return defaultValue;
    }
    return parsed as T;
  } catch {
    return defaultValue;
  }
}

export function writePersistentValue<T>(key: string, value: T): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(toStorageKey(key), JSON.stringify(value));
  } catch {
    // Ignore storage quota or private-mode restrictions.
  }
}

export function usePersistentState<T>(
  key: string,
  defaultValue: T,
  validate?: (value: unknown) => value is T,
): [T, (next: T | ((prev: T) => T)) => void] {
  const storageKey = toStorageKey(key);
  const [value, setValue] = useState<T>(() =>
    readPersistentValue(storageKey, defaultValue, validate),
  );

  const setPersistentValue = useCallback(
    (next: T | ((prev: T) => T)) => {
      setValue((prev) => {
        const resolved = typeof next === "function" ? (next as (p: T) => T)(prev) : next;
        writePersistentValue(storageKey, resolved);
        return resolved;
      });
    },
    [storageKey],
  );

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const handleStorage = (event: StorageEvent) => {
      if (event.key === storageKey || event.key === null) {
        setValue(readPersistentValue(storageKey, defaultValue, validate));
      }
    };
    window.addEventListener("storage", handleStorage);
    return () => {
      window.removeEventListener("storage", handleStorage);
    };
  }, [storageKey, defaultValue, validate]);

  return [value, setPersistentValue];
}
