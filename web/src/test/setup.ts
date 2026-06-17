import '@testing-library/jest-dom'

// jsdom does not implement the native <dialog> showModal/close methods (Modal.tsx uses them).
// Provide minimal stand-ins that toggle the `open` property so the component renders its body.
if (typeof HTMLDialogElement !== 'undefined') {
  if (!HTMLDialogElement.prototype.showModal) {
    HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
      this.open = true
    }
  }
  if (!HTMLDialogElement.prototype.close) {
    HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
      this.open = false
    }
  }
}
