// apps/conversations locale (en): all visible strings for the conversations tab.
export default {
  // New conversation button
  new: 'New chat',
  // Send button (idle)
  send: 'Send',
  // Send button (streaming in progress)
  sending: 'Sending…',
  // Input placeholder
  placeholder: 'Type a message…',
  // Rename action on session item
  rename: 'Rename',
  // Delete action on session item
  delete: 'Delete',
  // Empty state when no sessions exist
  empty: 'No conversations',
  // Attach file button label
  attach: 'File',
  // Send button while a reply is streaming (click enqueues instead of sending)
  queueSend: 'Queue',
  // Queued messages panel title
  queueTitle: 'Queued',
  // Status hint while a reply is generating (inside queue panel)
  generating: 'Generating…',
  // Edit action on a queued item
  queueEdit: 'Edit',
  // Save action in queued-item edit mode
  queueSave: 'Save',
  // Cancel action in queued-item edit mode
  queueCancel: 'Cancel',
  // Remove action on a queued item
  queueRemove: 'Remove',
  // Retry action on a failed queued item
  queueRetry: 'Retry',
  // Status badge on a failed queued item
  queueFailed: 'Failed',
  // ─── Voice input ─────────────────────────────────────────────────────────────
  voice: {
    // Mic button idle hint (start recording)
    start: 'Voice input',
    // Recording hint (click again to stop)
    recording: 'Recording, click to stop',
    // Transcribing hint
    transcribing: 'Transcribing…',
    // Model download progress (with percent param)
    downloading: 'Downloading model {percent}%',
    // Model picker popover title
    pickTitle: 'Choose speech model',
    // Download source group label
    sourceLabel: 'Download source',
    // Source option: domestic ModelScope
    sourceDomestic: 'ModelScope (CN)',
    // Source option: official site
    sourceOfficial: 'HuggingFace official',
    // Model tier group label
    tierLabel: 'Model size',
    // Tier hint: tiny
    tierTiny: 'Tiny (fastest, fair Chinese)',
    // Tier hint: base
    tierBase: 'Balanced (recommended)',
    // Tier hint: small
    tierSmall: 'Small (most accurate, largest/slowest)',
    // Tier hint: turbo (large-v3-turbo)
    tierTurbo: 'Turbo (best accuracy, needs WebGPU, ~760MB)',
    // Popover confirm button
    confirm: 'Download & use',
    // Switch model entry
    switch: 'Switch model',
    // Badge marking a tier already downloaded to local cache
    downloaded: 'Downloaded',
    // Error messages
    errors: {
      // Mic permission denied or insecure context
      permissionDenied: 'Cannot access microphone, check browser permissions',
      // Browser lacks required capability
      notSupported: 'Voice input is not supported in this browser',
      // No valid speech detected
      noSpeech: 'No speech detected',
      // Model download failed
      downloadFailed: 'Model download failed, try switching source',
      // Transcription error
      transcribeFailed: 'Speech recognition failed, please retry',
    },
  },
} as const
