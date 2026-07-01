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
} as const
