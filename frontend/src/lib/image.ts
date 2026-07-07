const MAX_IMAGE_PIXELS = 4096 * 2160
const MAX_IMAGE_SIDE = 4096
const JPEG_QUALITY = 0.9

type DecodedImage = {
  source: CanvasImageSource
  width: number
  height: number
  close: () => void
}

async function decodeImage(blob: Blob): Promise<DecodedImage> {
  if ('createImageBitmap' in window) {
    const bitmap = await createImageBitmap(blob)
    return { source: bitmap, width: bitmap.width, height: bitmap.height, close: () => bitmap.close() }
  }

  const url = URL.createObjectURL(blob)
  const img = new Image()
  img.decoding = 'async'
  img.src = url
  try {
    await img.decode()
  } catch (err) {
    URL.revokeObjectURL(url)
    throw err
  }
  return { source: img, width: img.naturalWidth, height: img.naturalHeight, close: () => URL.revokeObjectURL(url) }
}

function targetSize(width: number, height: number) {
  const scale = Math.min(1, MAX_IMAGE_SIDE / Math.max(width, height), Math.sqrt(MAX_IMAGE_PIXELS / (width * height)))
  return {
    width: Math.max(1, Math.floor(width * scale)),
    height: Math.max(1, Math.floor(height * scale)),
    scaled: scale < 1,
  }
}

async function drawToJpeg(decoded: DecodedImage, width: number, height: number): Promise<Blob | null> {
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext('2d')
  if (!ctx) return null
  ctx.drawImage(decoded.source, 0, 0, width, height)
  return await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', JPEG_QUALITY))
}

async function toJpegWithinKimiLimits(blob: Blob, forceJpeg: boolean): Promise<File | Blob | null> {
  let decoded: DecodedImage
  try {
    decoded = await decodeImage(blob)
  } catch {
    return null
  }

  try {
    const target = targetSize(decoded.width, decoded.height)
    if (!forceJpeg && !target.scaled) return blob
    return await drawToJpeg(decoded, target.width, target.height)
  } finally {
    decoded.close()
  }
}

// Safari/iOS decode HEIC natively at the OS level, which handles profiles
// (HDR, Live Photo main image, 10-bit) that the heic2any WASM fallback
// below doesn't. Try that path first via canvas; it's a silent no-op
// (returns null) on browsers with no native HEIC support.
async function heicToJpegViaCanvas(file: File): Promise<Blob | null> {
  return await toJpegWithinKimiLimits(file, true)
}

// iOS camera capture defaults to HEIC, which the backend can't decode.
// `file.type` is often empty for camera-captured HEIC in Safari, so also
// check the extension.
// Returns null if the file can't be converted — the server can't decode
// HEIC either, so there's no point uploading it and getting a 400 back.
export async function toUploadableImage(file: File): Promise<File | Blob | null> {
  const isHeic = file.type === 'image/heic' || file.type === 'image/heif' || /\.hei[cf]$/i.test(file.name)
  if (!isHeic) {
    return (await toJpegWithinKimiLimits(file, false)) ?? file
  }

  const viaCanvas = await heicToJpegViaCanvas(file)
  if (viaCanvas) return viaCanvas

  try {
    const heic2any = (await import('heic2any')).default
    const converted = await heic2any({ blob: file, toType: 'image/jpeg', quality: 0.9 })
    const blob = Array.isArray(converted) ? converted[0] : converted
    return await toJpegWithinKimiLimits(blob, true)
  } catch (err) {
    console.error('HEIC conversion failed', err)
    return null
  }
}
