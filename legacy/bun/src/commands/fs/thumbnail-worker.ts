import { optimizeImage } from 'wasm-image-optimization'

export interface ThumbnailWorkerRequest {
  id: number
  bytes: ArrayBuffer
}

export type ThumbnailWorkerResponse
  = | { id: number, ok: true, bytes: ArrayBuffer }
    | { id: number, ok: false, message: string }

const worker = globalThis as unknown as {
  onmessage: ((event: MessageEvent<ThumbnailWorkerRequest>) => void) | null
  postMessage: (message: ThumbnailWorkerResponse, transfer?: Transferable[]) => void
}

worker.onmessage = async (event: MessageEvent<ThumbnailWorkerRequest>) => {
  const { id, bytes } = event.data
  try {
    const result = await optimizeImage({
      image: bytes,
      width: 160,
      height: 160,
      fit: 'cover',
      format: 'webp',
      quality: 72,
      animation: false,
    })
    const output = new ArrayBuffer(result.data.byteLength)
    new Uint8Array(output).set(result.data)
    worker.postMessage({ id, ok: true, bytes: output } satisfies ThumbnailWorkerResponse, [output])
  }
  catch (cause) {
    worker.postMessage({
      id,
      ok: false,
      message: cause instanceof Error ? cause.message : String(cause),
    } satisfies ThumbnailWorkerResponse)
  }
}
