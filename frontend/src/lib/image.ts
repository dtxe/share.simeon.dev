// Safari/iOS decode HEIC natively at the OS level, which handles profiles
// (HDR, Live Photo main image, 10-bit) that the heic2any WASM fallback
// below doesn't. Try that path first via canvas; it's a silent no-op
// (returns null) on browsers with no native HEIC support.
async function heicToJpegViaCanvas(file: File): Promise<Blob | null> {
  try {
    const bitmap = await createImageBitmap(file)
    const canvas = document.createElement('canvas')
    canvas.width = bitmap.width
    canvas.height = bitmap.height
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    ctx.drawImage(bitmap, 0, 0)
    return await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.9))
  } catch {
    return null
  }
}

// iOS camera capture defaults to HEIC, which the backend can't decode.
// `file.type` is often empty for camera-captured HEIC in Safari, so also
// check the extension.
// Returns null if the file can't be converted — the server can't decode
// HEIC either, so there's no point uploading it and getting a 400 back.
export async function toUploadableImage(file: File): Promise<File | Blob | null> {
  const isHeic = file.type === 'image/heic' || file.type === 'image/heif' || /\.hei[cf]$/i.test(file.name)
  if (!isHeic) return file

  const viaCanvas = await heicToJpegViaCanvas(file)
  if (viaCanvas) return viaCanvas

  try {
    const heic2any = (await import('heic2any')).default
    const converted = await heic2any({ blob: file, toType: 'image/jpeg', quality: 0.9 })
    return Array.isArray(converted) ? converted[0] : converted
  } catch (err) {
    console.error('HEIC conversion failed', err)
    return null
  }
}
