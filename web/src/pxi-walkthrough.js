// web/src/pxi-walkthrough.js
// pxi around px0's Review walkthrough (web/src/reviewwalkthrough.js):
// - which changes it shows: the open folder's (px0's default) or one
//   worktree's against its base (/api/review/walkthrough?wt=<name>,
//   fork_understanding.go), opened from a worktree card;
// - leaving the review screen drops the walkthrough's cards, diff rows and data,
//   so a closed review keeps nothing in memory.
// Top-level names are prefixed pxw (Node fallback bundler).
import { $ } from './state.js';
import { loadReviewWalkthrough, setWalkthroughSubTab, pxiDropWalkthrough } from './reviewwalkthrough.js';

let pxwTarget = '';

/** Query for /api/review/walkthrough: a worktree, or none for the open folder. */
export function pxiWalkthroughParams() {
  return pxwTarget ? { wt: pxwTarget } : undefined;
}

/** Opens the review screen's walkthrough on one worktree's changes. */
export function openWorktreeWalkthrough(name) {
  pxwTarget = name;
  pxiDropWalkthrough();
  const reviewBtn = $('#btn-mode-review');
  if (reviewBtn && !reviewBtn.classList.contains('active')) {
    setWalkthroughSubTab('walkthrough');
    reviewBtn.click(); // px0's setMode loads the walkthrough
  } else {
    setWalkthroughSubTab('walkthrough');
    loadReviewWalkthrough();
  }
}

export function initPxiWalkthrough() {
  let inReview = document.body.classList.contains('in-review-mode');
  new MutationObserver(() => {
    const now = document.body.classList.contains('in-review-mode');
    if (inReview && !now) {
      pxwTarget = '';
      pxiDropWalkthrough();
      for (const id of ['#review-understanding-content', '#review-diff-content', '#review-understanding-files-nav']) $(id)?.replaceChildren();
    }
    inReview = now;
  }).observe(document.body, { attributes: true, attributeFilter: ['class'] });
}
