const MAX_IMAGE_PIXELS = 4096 * 2160
const MAX_IMAGE_SIDE = 4096
const JPEG_QUALITY = 0.95

type DecodedImage = {
  source: CanvasImageSource
  width: number
  height: number
  close: () => void
}

async function decodeImage(blob: Blob): Promise<DecodedImage> {
  // createImageBitmap is the fast path, but on iOS Safari it exists yet
  // can't decode HEIC — it throws rather than falling back. Catch that and
  // route through <img>.decode() below, which is the only path that reaches
  // iOS's OS-level native HEIC decoder. Without this fallback the HEIC→canvas
  // conversion silently returns null on iPhone and the upload fails.
  if ('createImageBitmap' in window) {
    try {
      const bitmap = await createImageBitmap(blob)
      return { source: bitmap, width: bitmap.width, height: bitmap.height, close: () => bitmap.close() }
    } catch {
      // unsupported format for createImageBitmap (e.g. HEIC on iOS); fall through
    }
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
// below doesn't. Try native decoding first via canvas; it's a silent no-op
// (returns null) on browsers with no native HEIC/AVIF support.
async function isoBmffToJpegViaCanvas(file: File): Promise<Blob | null> {
  return await toJpegWithinKimiLimits(file, true)
}

// iOS camera capture defaults to HEIC. Convert it in-browser when possible
// to avoid uploading the larger original; the backend is the final fallback.
// `file.type` is often empty for camera-captured HEIC in Safari, so sniff
// the ftyp box too — extension alone misses mislabeled library exports.
// The server remains the final fallback if every client-side decoder fails.
const ISO_BMFF_BRANDS = new Set([
  'heic',
  'heix',
  'hevc',
  'hevx',
  'mif1',
  'mif3',
  'msf1',
  'heim',
  'heis',
  'avif',
  'avis',
  'avic',
])

type IsoBmffImage = 'heic' | 'avif'

async function detectIsoBmffImage(file: File): Promise<IsoBmffImage | null> {
  if (file.type === 'image/heic' || file.type === 'image/heif' || /\.hei[cf]$/i.test(file.name)) {
    return 'heic'
  }
  if (file.type === 'image/avif' || /\.avif$/i.test(file.name)) {
    return 'avif'
  }
  // ftyp is normally the first box. Read only a small bounded prefix and
  // inspect both major_brand and compatible_brands, rather than assuming a
  // particular brand is always at bytes 8-12.
  const prefix = new Uint8Array(await file.slice(0, Math.min(file.size, 64 * 1024)).arrayBuffer())
  if (prefix.byteLength < 16) return null
  const view = new DataView(prefix.buffer, prefix.byteOffset, prefix.byteLength)
  if (view.getUint32(4, false) !== 0x66747970) return null // "ftyp"

  const declaredSize = view.getUint32(0, false)
  let boxEnd = declaredSize
  let brandsOffset = 8
  if (declaredSize === 1) {
    if (prefix.byteLength < 24) return null
    const extendedSize = view.getBigUint64(8, false)
    if (extendedSize > BigInt(Number.MAX_SAFE_INTEGER)) return null
    boxEnd = Number(extendedSize)
    brandsOffset = 16
    if (boxEnd < 24 || boxEnd > file.size) return null
  } else if (boxEnd !== 0) {
    if (boxEnd < 16 || boxEnd > file.size) return null
  }
  if (boxEnd === 0) boxEnd = file.size
  const safeBoxEnd = Math.min(boxEnd, prefix.byteLength)
  if (safeBoxEnd < brandsOffset + 8) return null

  let heic = false
  let avif = false
  const brandAt = (offset: number) => new TextDecoder().decode(prefix.slice(offset, offset + 4))
  const majorBrand = brandAt(brandsOffset)
  if (ISO_BMFF_BRANDS.has(majorBrand)) {
    if (majorBrand === 'avif' || majorBrand === 'avis' || majorBrand === 'avic') avif = true
    else heic = true
  }
  // Skip minor_version; compatible brands begin after major_brand + minor_version.
  for (let offset = brandsOffset + 8; offset + 4 <= safeBoxEnd; offset += 4) {
    const brand = brandAt(offset)
    if (!ISO_BMFF_BRANDS.has(brand)) continue
    if (brand === 'avif' || brand === 'avis' || brand === 'avic') avif = true
    else heic = true
  }
  return avif ? 'avif' : heic ? 'heic' : null
}

export async function toUploadableImage(file: File): Promise<File | Blob> {
  const isoBmffImage = await detectIsoBmffImage(file)
  if (isoBmffImage) {
    const viaCanvas = await isoBmffToJpegViaCanvas(file)
    if (viaCanvas) return viaCanvas

    if (isoBmffImage === 'heic') {
      try {
        const heic2any = (await import('heic2any')).default
        const converted = await heic2any({ blob: file, toType: 'image/jpeg', quality: JPEG_QUALITY })
        const blob = Array.isArray(converted) ? converted[0] : converted
        const convertedJpeg = await toJpegWithinKimiLimits(blob, true)
        if (convertedJpeg) return convertedJpeg
      } catch (err) {
        console.error('HEIC/HEIF conversion failed', err)
      }
    }
    return file
  }
  return (await toJpegWithinKimiLimits(file, false)) ?? file
}
