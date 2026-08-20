/* SPDX-License-Identifier: MIT */

/**
 * GoPDS Client Application
 */
const FIELDS = [
    { key: 'title', label: 'Title', type: 'input' },
    { key: 'author', label: 'Author', type: 'input' },
    { key: 'language', label: 'Language', type: 'input' },
    { key: 'identifier', label: 'Identifier (ISBN/ID)', type: 'input' },
    { key: 'publisher', label: 'Publisher', type: 'input' },
    { key: 'date', label: 'Publication Date', type: 'input' },
    { key: 'series', label: 'Series', type: 'input' },
    { key: 'series_index', label: 'Series Index', type: 'input' },
    { key: 'subjects', label: 'Subjects (comma-separated)', type: 'input' },
    { key: 'description', label: 'Description', type: 'textarea' }
];

// Self-contained placeholder for books with no cover art. Inlined as a data
// URI so the grid never depends on a third-party image host being reachable.
const COVER_PLACEHOLDER = 'data:image/svg+xml;charset=UTF-8,' + encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 300">' +
    '<rect width="200" height="300" fill="#212c36"/>' +
    '<rect x="0.5" y="0.5" width="199" height="299" fill="none" stroke="#2f3d49"/>' +
    '<path d="M100 118a20 20 0 1 1 0 40 20 20 0 0 1 0-40zm0 8a12 12 0 1 0 0 24 12 12 0 0 0 0-24z" fill="#3d4c5a"/>' +
    '<rect x="52" y="176" width="96" height="7" rx="3.5" fill="#3d4c5a"/>' +
    '<rect x="68" y="192" width="64" height="7" rx="3.5" fill="#33414e"/>' +
    '<text x="100" y="232" font-family="Segoe UI,Roboto,Helvetica,Arial,sans-serif" ' +
    'font-size="13" fill="#6b7d8c" text-anchor="middle">No Cover</text></svg>'
);

// Trailing "Series Name Book 3" marker used to group a series in the grid.
const SERIES_NAMED_RE = /\s*[\(\[]\s*([^)\]]*?)[,\s]+(?:#|book\s*|vol\.?\s*|part\s*)\s*(\d+(?:\.\d+)?)\s*[\)\]]\s*$/i;

const App = {
    COVER_PLACEHOLDER,
    groupSeries: true,
    expandedSeries: {},
    allBooks: [],
    filteredBooks: [],
    currentIndex: 0,
    itemsPerPage: 50,
    modalBookId: null,
    infoModalBookId: null,
    coverModalBookId: null,
    openLibraryResults: [],
    selectedOpenLibrary: null,
    coverCandidatesByKey: {},
    rebuildPollTimer: null,
    lastRebuildCompletedAt: '',
    coverVersion: {},
    filterAuthor: '__all',
    filterCategory: '__all',
    filterSubcategory: '__all',
    reviewItems: [],
    returnToReview: false,
    reviewPage: 1,
    reviewTotal: 0,
    reviewFlag: '',
    enrichPollTimer: null,
    auth: {
        authenticated: false,
        username: ''
    },

    ui: {
        library: document.getElementById('library'),
        search: document.getElementById('search'),
        authorFilter: document.getElementById('author-filter'),
        categoryFilter: document.getElementById('category-filter'),
        subcategoryFilter: document.getElementById('subcategory-filter'),
        rescanBtn: document.getElementById('rescan-btn'),
        rebuildBtn: document.getElementById('rebuild-btn'),
        rebuildStatus: document.getElementById('rebuild-status'),
        authBtn: document.getElementById('auth-btn'),
        authStatus: document.getElementById('auth-status'),
        reviewBtn: document.getElementById('review-btn'),
        groupSeriesToggle: document.getElementById('group-series')
    },

    async init() {
        this.createModal();
        this.createInfoModal();
        this.createCoverModal();
        this.createReviewModal();
        this.bindEvents();
        await this.syncAuthStatus();
        await this.fetchLibrary();
        await this.syncRebuildStatus();
    },

    createModal() {
        const modal = document.createElement('div');
        modal.id = 'editor-modal';
        modal.className = 'modal hidden';

        const fieldRows = FIELDS.map((f) => {
            const input = f.type === 'textarea'
                ? `<textarea data-field="${f.key}"></textarea>`
                : `<input data-field="${f.key}">`;
            return `
                <div class="field-row" data-field-row="${f.key}">
                    <label class="field-label">${f.label}</label>
                    <div class="field-local">${input}</div>
                    <div class="field-remote" data-remote="${f.key}">-</div>
                    <button type="button" class="field-apply" data-apply-field="${f.key}">Use</button>
                </div>
            `;
        }).join('');

        modal.innerHTML = `
            <div class="modal-backdrop" data-close-modal="1"></div>
            <div class="modal-dialog" role="dialog" aria-modal="true" aria-label="Edit EPUB metadata">
                <div class="modal-header">
                    <h2>Edit EPUB Metadata</h2>
                    <button type="button" id="modal-close" class="modal-close" aria-label="Close">&times;</button>
                </div>
                <div class="modal-book" id="modal-book"></div>

                <div class="ol-controls">
                    <input id="ol-query" placeholder="Search Open Library (title + author)...">
                    <button type="button" id="ol-fetch">Fetch Open Library</button>
                </div>
                <div class="edit-status" id="ol-status"></div>
                <div id="ol-results" class="ol-results"></div>

                <form id="modal-edit-form" class="modal-form">
                    <div class="field-table-header">
                        <span>Field</span>
                        <span>Local EPUB Value</span>
                        <span>Open Library Value</span>
                        <span>Apply</span>
                    </div>
                    <div class="field-table-body">${fieldRows}</div>
                    <div class="modal-actions">
                        <button type="submit" id="modal-save">Save All Fields to EPUB</button>
                        <div class="edit-status" id="modal-status"></div>
                    </div>
                </form>
            </div>
        `;

        document.body.appendChild(modal);

        this.ui.modal = modal;
        this.ui.modalClose = modal.querySelector('#modal-close');
        this.ui.modalBook = modal.querySelector('#modal-book');
        this.ui.modalForm = modal.querySelector('#modal-edit-form');
        this.ui.modalSave = modal.querySelector('#modal-save');
        this.ui.modalStatus = modal.querySelector('#modal-status');
        this.ui.olQuery = modal.querySelector('#ol-query');
        this.ui.olFetch = modal.querySelector('#ol-fetch');
        this.ui.olStatus = modal.querySelector('#ol-status');
        this.ui.olResults = modal.querySelector('#ol-results');
        this.ui.fieldInputs = {};
        FIELDS.forEach((f) => {
            this.ui.fieldInputs[f.key] = modal.querySelector(`[data-field="${f.key}"]`);
        });
    },

    bindEvents() {
        this.ui.search.addEventListener('input', (e) => this.handleSearch(e));
        this.ui.authorFilter.addEventListener('change', (e) => this.handleAuthorFilterChange(e));
        this.ui.categoryFilter.addEventListener('change', (e) => this.handleCategoryFilterChange(e));
        this.ui.subcategoryFilter.addEventListener('change', (e) => this.handleSubcategoryFilterChange(e));
        window.addEventListener('scroll', () => this.handleScroll());
        this.ui.rescanBtn.addEventListener('click', () => this.handleRescanClick());
        this.ui.rebuildBtn.addEventListener('click', () => this.handleRebuildClick());
        this.ui.authBtn.addEventListener('click', () => this.handleAuthClick());
        this.ui.reviewBtn.addEventListener('click', () => this.openReviewModal());
        this.ui.groupSeriesToggle.addEventListener('change', (e) => {
            this.groupSeries = e.target.checked;
            this.expandedSeries = {};
            this.render(true);
        });

        this.ui.library.addEventListener('click', (e) => this.handleLibraryClick(e));

        this.ui.infoModal.addEventListener('click', (e) => {
            if (e.target.dataset.closeInfoModal === '1') {
                this.closeInfoModal();
            }
        });
        this.ui.infoModalClose.addEventListener('click', () => this.closeInfoModal());

        this.ui.modal.addEventListener('click', (e) => {
            if (e.target.dataset.closeModal === '1') {
                this.closeModal();
                return;
            }

            const applyBtn = e.target.closest('[data-apply-field]');
            if (applyBtn) {
                this.applyRemoteField(applyBtn.dataset.applyField);
                return;
            }

            const selectBtn = e.target.closest('[data-result-index]');
            if (selectBtn) {
                const idx = Number(selectBtn.dataset.resultIndex);
                this.selectOpenLibraryResult(idx);
            }
        });

        this.ui.modalClose.addEventListener('click', () => this.closeModal());
        this.ui.modalForm.addEventListener('submit', (e) => this.handleMetadataSubmit(e));
        this.ui.olFetch.addEventListener('click', () => this.fetchOpenLibrary());
        this.ui.coverModalClose.addEventListener('click', () => this.closeCoverModal());
        this.ui.coverModal.addEventListener('click', (e) => {
            if (e.target.dataset.closeCoverModal === '1') {
                this.closeCoverModal();
            }
        });
        this.ui.coverModalApply.addEventListener('click', () => this.applyCoverSelection());
        this.ui.coverFetchOnline.addEventListener('click', () => this.fetchOnlineCoverCandidates());

        // Escape closes the topmost open dialog only. Chained else-if matters
        // here: closing an editor can reveal the review queue underneath, and
        // a second branch would immediately close that too.
        document.addEventListener('keydown', (e) => {
            if (e.key !== 'Escape') {
                return;
            }
            if (!this.ui.modal.classList.contains('hidden')) {
                this.closeModal();
            } else if (!this.ui.coverModal.classList.contains('hidden')) {
                this.closeCoverModal();
            } else if (!this.ui.infoModal.classList.contains('hidden')) {
                this.closeInfoModal();
            } else if (this.ui.reviewModal && !this.ui.reviewModal.classList.contains('hidden')) {
                this.closeReviewModal();
            }
        });
    },

    createInfoModal() {
        const modal = document.createElement('div');
        modal.id = 'book-info-modal';
        modal.className = 'modal hidden';
        modal.innerHTML = `
            <div class="modal-backdrop" data-close-info-modal="1"></div>
            <div class="modal-dialog info-modal-dialog" role="dialog" aria-modal="true" aria-label="Book info">
                <div class="modal-header">
                    <h2>Book Info</h2>
                    <button type="button" id="info-modal-close" class="modal-close" aria-label="Close">&times;</button>
                </div>
                <div class="modal-book" id="info-modal-book"></div>
                <div class="info-grid" id="info-modal-fields"></div>
            </div>
        `;

        document.body.appendChild(modal);
        this.ui.infoModal = modal;
        this.ui.infoModalClose = modal.querySelector('#info-modal-close');
        this.ui.infoModalBook = modal.querySelector('#info-modal-book');
        this.ui.infoModalFields = modal.querySelector('#info-modal-fields');
    },

    createCoverModal() {
        const modal = document.createElement('div');
        modal.id = 'cover-modal';
        modal.className = 'modal hidden';
        modal.innerHTML = `
            <div class="modal-backdrop" data-close-cover-modal="1"></div>
            <div class="modal-dialog cover-modal-dialog" role="dialog" aria-modal="true" aria-label="Change cover">
                <div class="modal-header">
                    <h2>Change Cover</h2>
                    <button type="button" id="cover-modal-close" class="modal-close" aria-label="Close">&times;</button>
                </div>
                <div class="modal-book" id="cover-modal-book"></div>
                <div class="cover-status" id="cover-modal-status">Loading cover candidates...</div>
                <div class="cover-online-actions">
                    <button type="button" id="cover-fetch-online">Find Online Covers</button>
                </div>
                <div class="cover-grid" id="cover-grid"></div>
                <label class="cover-write-flag">
                    <input type="checkbox" id="cover-write-epub">
                    Also write selected cover into EPUB file
                </label>
                <div class="modal-actions">
                    <button type="button" id="cover-apply">Apply Cover</button>
                </div>
            </div>
        `;

        document.body.appendChild(modal);
        this.ui.coverModal = modal;
        this.ui.coverModalClose = modal.querySelector('#cover-modal-close');
        this.ui.coverModalBook = modal.querySelector('#cover-modal-book');
        this.ui.coverModalStatus = modal.querySelector('#cover-modal-status');
        this.ui.coverFetchOnline = modal.querySelector('#cover-fetch-online');
        this.ui.coverGrid = modal.querySelector('#cover-grid');
        this.ui.coverWriteEPUB = modal.querySelector('#cover-write-epub');
        this.ui.coverModalApply = modal.querySelector('#cover-apply');
    },

    async fetchLibrary() {
        try {
            const response = await fetch('/api/books');
            if (!response.ok) {
                throw new Error(`Failed to load books (${response.status})`);
            }

            this.allBooks = await response.json();
            this.ui.search.placeholder = `Search ${this.allBooks.length} books...`;
            this.refreshBrowseFilters();
            this.applyFiltersAndRender();
        } catch (err) {
            this.ui.library.innerText = 'Error loading library. Check console.';
            console.error(err);
        }
    },

    async handleRebuildClick() {
        if (!this.auth.authenticated) {
            this.ui.rebuildStatus.textContent = 'Admin login required.';
            return;
        }

        const confirmed = window.confirm(
            'This will delete the book cache database and covers cache, then rebuild everything from disk. Continue?'
        );
        if (!confirmed) {
            return;
        }

        this.ui.rebuildStatus.textContent = 'Starting rebuild...';

        try {
            await this.startAdminScan('/api/admin/rebuild', 'Rebuild');
        } catch (err) {
            this.ui.rebuildStatus.textContent = `Rebuild failed: ${err.message}`;
            this.stopRebuildPolling();
            console.error(err);
        }
    },

    async handleRescanClick() {
        if (!this.auth.authenticated) {
            this.ui.rebuildStatus.textContent = 'Admin login required.';
            return;
        }

        this.ui.rebuildStatus.textContent = 'Starting rescan...';
        try {
            await this.startAdminScan('/api/admin/rescan', 'Rescan');
        } catch (err) {
            this.ui.rebuildStatus.textContent = `Rescan failed: ${err.message}`;
            this.stopRebuildPolling();
            console.error(err);
        }
    },

    async startAdminScan(endpoint, label) {
        const response = await fetch(endpoint, { method: 'POST' });
        if (!response.ok) {
            const msg = await response.text();
            throw new Error(msg || `${label} failed (${response.status})`);
        }
        const payload = await response.json();
        this.applyRebuildStatus(payload);
        this.startRebuildPolling();
    },

    async syncRebuildStatus() {
        if (!this.auth.authenticated) {
            this.stopRebuildPolling();
            this.ui.rebuildStatus.textContent = '';
            this.ui.rescanBtn.classList.add('hidden');
            this.ui.rebuildBtn.classList.add('hidden');
            return;
        }

        try {
            const response = await fetch('/api/admin/rebuild/status');
            if (!response.ok) {
                if (response.status === 401) {
                    await this.syncAuthStatus();
                }
                return;
            }
            const status = await response.json();
            this.applyRebuildStatus(status);
            if (status.running) {
                this.startRebuildPolling();
            }
        } catch (err) {
            console.error(err);
        }
    },

    async syncAuthStatus() {
        try {
            const response = await fetch('/api/auth/status');
            if (!response.ok) {
                this.auth = { authenticated: false, username: '' };
            } else {
                const payload = await response.json();
                this.auth = {
                    authenticated: Boolean(payload.authenticated),
                    username: payload.username || ''
                };
            }
        } catch (err) {
            console.error(err);
            this.auth = { authenticated: false, username: '' };
        }

        this.renderAuthState();
        this.render(true);
    },

    renderAuthState() {
        if (this.auth.authenticated) {
            this.ui.authBtn.textContent = 'Logout';
            this.ui.authStatus.textContent = `Logged in as ${this.auth.username || 'admin'}.`;
            this.ui.rescanBtn.classList.remove('hidden');
            this.ui.rebuildBtn.classList.remove('hidden');
            this.ui.reviewBtn.classList.remove('hidden');
            return;
        }
        this.ui.authBtn.textContent = 'Admin Login';
        this.ui.authStatus.textContent = 'Read-only mode.';
        this.ui.rescanBtn.classList.add('hidden');
        this.ui.rebuildBtn.classList.add('hidden');
        this.ui.reviewBtn.classList.add('hidden');
        this.ui.rebuildStatus.textContent = '';
    },

    async handleAuthClick() {
        if (this.auth.authenticated) {
            try {
                await fetch('/api/auth/logout', { method: 'POST' });
            } catch (err) {
                console.error(err);
            }
            await this.syncAuthStatus();
            return;
        }

        const usernameInput = window.prompt('Username', 'admin');
        if (usernameInput === null) {
            return;
        }
        const passwordInput = window.prompt('Password');
        if (passwordInput === null) {
            return;
        }

        this.ui.authStatus.textContent = 'Signing in...';
        try {
            const response = await fetch('/api/auth/login', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    username: usernameInput.trim() || 'admin',
                    password: passwordInput
                })
            });
            if (!response.ok) {
                const msg = await response.text();
                throw new Error(msg || `Login failed (${response.status})`);
            }
            await this.syncAuthStatus();
        } catch (err) {
            this.ui.authStatus.textContent = `Login failed: ${err.message}`;
            console.error(err);
        }
    },

    startRebuildPolling() {
        if (this.rebuildPollTimer) {
            return;
        }
        this.rebuildPollTimer = window.setInterval(() => {
            this.syncRebuildStatus();
        }, 2000);
    },

    stopRebuildPolling() {
        if (!this.rebuildPollTimer) {
            return;
        }
        window.clearInterval(this.rebuildPollTimer);
        this.rebuildPollTimer = null;
    },

    applyRebuildStatus(status) {
        if (!status || typeof status !== 'object') {
            return;
        }

        const startedAt = status.started_at ? new Date(status.started_at) : null;
        if (status.running) {
            this.ui.rescanBtn.disabled = true;
            this.ui.rebuildBtn.disabled = true;
            const elapsed = startedAt ? Math.max(0, Math.floor((Date.now() - startedAt.getTime()) / 1000)) : null;
            const elapsedText = elapsed === null ? '' : ` (${elapsed}s elapsed)`;
            this.ui.rebuildStatus.textContent = `${status.message || 'Rebuild running...'}${elapsedText}`;
            return;
        }

        this.ui.rescanBtn.disabled = false;
        this.ui.rebuildBtn.disabled = false;
        this.stopRebuildPolling();

        if (status.phase === 'failed') {
            this.ui.rebuildStatus.textContent = status.error || status.message || 'Rebuild failed.';
            return;
        }

        if (status.phase === 'complete') {
            const completedAt = status.completed_at || '';
            if (completedAt && completedAt !== this.lastRebuildCompletedAt) {
                this.lastRebuildCompletedAt = completedAt;
                this.ui.search.value = '';
                this.fetchLibrary();
            }
            this.ui.rebuildStatus.textContent = status.message || `Rebuild complete. ${status.count || 0} books indexed.`;
            return;
        }

        this.ui.rebuildStatus.textContent = status.message || '';
    },

    handleSearch(e) {
        this.applyFiltersAndRender();
    },

    handleAuthorFilterChange(e) {
        this.filterAuthor = e.target.value || '__all';
        this.applyFiltersAndRender();
    },

    handleCategoryFilterChange(e) {
        this.filterCategory = e.target.value || '__all';
        this.filterSubcategory = '__all';
        this.refreshSubcategoryFilter();
        this.applyFiltersAndRender();
    },

    handleSubcategoryFilterChange(e) {
        this.filterSubcategory = e.target.value || '__all';
        this.applyFiltersAndRender();
    },

    refreshBrowseFilters() {
        this.refreshAuthorFilter();
        this.refreshCategoryFilter();
        this.refreshSubcategoryFilter();
    },

    refreshAuthorFilter() {
        const authors = Array.from(
            new Set(
                this.allBooks
                    .map((b) => (b.author || '').trim())
                    .filter(Boolean)
            )
        ).sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }));

        const hadUnknown = this.allBooks.some((b) => !(b.author || '').trim());
        const selected = this.filterAuthor;

        const options = ['<option value="__all">All authors</option>'];
        if (hadUnknown) {
            options.push('<option value="__unknown">Unknown author</option>');
        }
        authors.forEach((a) => {
            options.push(`<option value="${this.escapeHTML(a)}">${this.escapeHTML(a)}</option>`);
        });
        this.ui.authorFilter.innerHTML = options.join('');

        const canRestore = selected === '__all'
            || (selected === '__unknown' && hadUnknown)
            || authors.includes(selected);
        this.filterAuthor = canRestore ? selected : '__all';
        this.ui.authorFilter.value = this.filterAuthor;
    },

    refreshCategoryFilter() {
        const categories = Array.from(
            new Set(
                this.allBooks
                    .map((b) => (b.category || '').trim())
                    .filter(Boolean)
            )
        ).sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }));

        const hadUncategorized = this.allBooks.some((b) => !(b.category || '').trim());
        const selected = this.filterCategory;

        const options = ['<option value="__all">All categories</option>'];
        if (hadUncategorized) {
            options.push('<option value="__uncat">Uncategorized</option>');
        }
        categories.forEach((c) => {
            options.push(`<option value="${this.escapeHTML(c)}">${this.escapeHTML(c)}</option>`);
        });

        this.ui.categoryFilter.innerHTML = options.join('');
        const canRestore = selected === '__all'
            || (selected === '__uncat' && hadUncategorized)
            || categories.includes(selected);
        this.filterCategory = canRestore ? selected : '__all';
        this.ui.categoryFilter.value = this.filterCategory;
    },

    refreshSubcategoryFilter() {
        const selectedCategory = this.filterCategory;
        if (selectedCategory === '__all' || selectedCategory === '__uncat') {
            this.ui.subcategoryFilter.innerHTML = '<option value="__all">All sub-categories</option>';
            this.ui.subcategoryFilter.disabled = true;
            this.filterSubcategory = '__all';
            return;
        }

        const inCategory = this.allBooks.filter((b) => ((b.category || '').trim()) === selectedCategory);
        const subcategories = Array.from(
            new Set(inCategory.map((b) => (b.subcategory || '').trim()).filter(Boolean))
        ).sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }));
        const hasNoSubcategory = inCategory.some((b) => !(b.subcategory || '').trim());

        const selected = this.filterSubcategory;
        const options = ['<option value="__all">All sub-categories</option>'];
        if (hasNoSubcategory) {
            options.push('<option value="__none">No sub-category</option>');
        }
        subcategories.forEach((s) => {
            options.push(`<option value="${this.escapeHTML(s)}">${this.escapeHTML(s)}</option>`);
        });
        this.ui.subcategoryFilter.innerHTML = options.join('');
        this.ui.subcategoryFilter.disabled = false;

        const canRestore = selected === '__all'
            || (selected === '__none' && hasNoSubcategory)
            || subcategories.includes(selected);
        this.filterSubcategory = canRestore ? selected : '__all';
        this.ui.subcategoryFilter.value = this.filterSubcategory;
    },

    applyFiltersAndRender() {
        const term = (this.ui.search.value || '').toLowerCase();
        this.filteredBooks = this.allBooks.filter((b) => {
            const author = (b.author || '').trim();
            const category = (b.category || '').trim();
            const subcategory = (b.subcategory || '').trim();

            let authorMatch = true;
            if (this.filterAuthor === '__unknown') {
                authorMatch = author === '';
            } else if (this.filterAuthor !== '__all') {
                authorMatch = author === this.filterAuthor;
            }
            if (!authorMatch) {
                return false;
            }

            let categoryMatch = true;
            if (this.filterCategory === '__uncat') {
                categoryMatch = category === '';
            } else if (this.filterCategory !== '__all') {
                categoryMatch = category === this.filterCategory;
            }
            if (!categoryMatch) {
                return false;
            }

            let subcategoryMatch = true;
            if (this.filterSubcategory === '__none') {
                subcategoryMatch = subcategory === '';
            } else if (this.filterSubcategory !== '__all') {
                subcategoryMatch = subcategory === this.filterSubcategory;
            }
            if (!subcategoryMatch) {
                return false;
            }

            if (!term) {
                return true;
            }
            return (b.title || '').toLowerCase().includes(term) ||
                (b.author || '').toLowerCase().includes(term);
        });
        this.render(true);
    },

    handleScroll() {
        const threshold = 800;
        if ((window.innerHeight + window.scrollY) >= document.body.offsetHeight - threshold) {
            if (this.currentIndex < this.filteredBooks.length) {
                this.render(false);
            }
        }
    },

    handleLibraryClick(e) {
        const seriesToggle = e.target.closest('[data-series-toggle]');
        if (seriesToggle) {
            const key = seriesToggle.dataset.seriesToggle;
            this.expandedSeries[key] = !this.expandedSeries[key];
            this.render(true);
            return;
        }

        const infoButton = e.target.closest('.book-info');
        if (infoButton) {
            const id = Number(infoButton.dataset.bookId);
            const book = this.allBooks.find((b) => b.id === id);
            if (!book) {
                return;
            }
            this.openInfoModal(book);
            return;
        }

        const editButton = e.target.closest('.edit-toggle');
        if (editButton) {
            if (!this.auth.authenticated) {
                return;
            }
            const id = Number(editButton.dataset.bookId);
            const book = this.allBooks.find((b) => b.id === id);
            if (!book) {
                return;
            }
            this.openModal(book);
            return;
        }

        const coverButton = e.target.closest('.change-cover');
        if (!coverButton) {
            return;
        }
        if (!this.auth.authenticated) {
            return;
        }

        const id = Number(coverButton.dataset.bookId);
        const book = this.allBooks.find((b) => b.id === id);
        if (!book) {
            return;
        }
        this.openCoverModal(book);
    },

    async openCoverModal(book) {
        if (!this.auth.authenticated) {
            this.ui.authStatus.textContent = 'Admin login required.';
            return;
        }
        this.coverModalBookId = book.id;
        this.coverCandidatesByKey = {};
        this.ui.coverModalBook.textContent = `Book #${book.id} | ${book.title || 'Untitled'}`;
        this.ui.coverModalStatus.textContent = 'Loading cover candidates...';
        this.ui.coverGrid.innerHTML = '';
        this.ui.coverWriteEPUB.checked = false;
        this.ui.coverModal.classList.remove('hidden');

        try {
            const response = await fetch(`/api/books/${book.id}/covers/candidates`);
            if (!response.ok) {
                if (response.status === 401) {
                    await this.syncAuthStatus();
                }
                const msg = await response.text();
                throw new Error(msg || `Cover candidate lookup failed (${response.status})`);
            }
            const payload = await response.json();
            const candidates = payload.candidates || [];
            this.coverCandidatesByKey = {};
            if (candidates.length === 0) {
                this.ui.coverModalStatus.textContent = 'No suitable cover images found in this EPUB.';
                this.ui.coverModalApply.disabled = true;
                return;
            }

            this.ui.coverModalApply.disabled = false;
            this.renderCoverCandidates(candidates);
            this.ui.coverModalStatus.textContent = `Loaded ${candidates.length} cover candidates.`;
        } catch (err) {
            this.ui.coverModalStatus.textContent = `Error: ${err.message}`;
            this.ui.coverModalApply.disabled = true;
            console.error(err);
        }
    },

    openInfoModal(book) {
        this.infoModalBookId = book.id;
        this.ui.infoModalBook.textContent = `Book #${book.id} | ${book.title || 'Untitled'}`;

        const rows = [
            ['Title', book.title || '-'],
            ['Author', book.author || '-'],
            ['Category', book.category || '-'],
            ['Sub-category', book.subcategory || '-'],
            ['Description', book.description || '-'],
            ['Path', book.path || '-'],
            ['Modified', this.formatDateTime(book.mod_time)]
        ];

        this.ui.infoModalFields.innerHTML = rows.map(([label, value]) => `
            <div class="info-row">
                <div class="info-label">${this.escapeHTML(label)}</div>
                <div class="info-value">${this.escapeHTML(value)}</div>
            </div>
        `).join('');
        this.ui.infoModal.classList.remove('hidden');
    },

    closeInfoModal() {
        this.infoModalBookId = null;
        this.ui.infoModal.classList.add('hidden');
    },

    closeCoverModal() {
        this.coverModalBookId = null;
        this.coverCandidatesByKey = {};
        this.ui.coverModal.classList.add('hidden');
        this.restoreReviewModal();
    },

    renderCoverCandidates(candidates) {
        this.coverCandidatesByKey = {};
        candidates.forEach((c) => {
            if (c && c.key) {
                this.coverCandidatesByKey[c.key] = c;
            }
        });

        this.ui.coverGrid.innerHTML = candidates.map((c, idx) => `
            <label class="cover-option">
                <input type="radio" name="cover-candidate" value="${this.escapeHTML(c.key)}" ${c.is_current || idx === 0 ? 'checked' : ''}>
                <img src="${this.escapeHTML(c.preview_url)}" alt="${this.escapeHTML(c.name)}">
                <span>${this.escapeHTML(c.name)}</span>
                <small>${c.width > 0 && c.height > 0 ? `${c.width}x${c.height} ` : ''}${this.escapeHTML(c.media_type || '')}${c.source ? ` | ${this.escapeHTML(c.source)}` : ''}${c.is_current ? ' | current' : ''}</small>
            </label>
        `).join('');
    },

    async fetchOnlineCoverCandidates() {
        if (!this.coverModalBookId) {
            return;
        }
        console.info('[covers] online lookup start', {
            bookId: this.coverModalBookId
        });
        this.ui.coverFetchOnline.disabled = true;
        this.ui.coverModalStatus.textContent = 'Searching online covers (Wikipedia/Open Library)...';
        try {
            const response = await fetch(`/api/books/${this.coverModalBookId}/covers/online`);
            console.info('[covers] online lookup response', {
                bookId: this.coverModalBookId,
                status: response.status,
                ok: response.ok
            });
            if (!response.ok) {
                const msg = await response.text();
                console.warn('[covers] online lookup failed', {
                    bookId: this.coverModalBookId,
                    status: response.status,
                    message: msg
                });
                throw new Error(msg || `Online cover lookup failed (${response.status})`);
            }
            const payload = await response.json();
            const incoming = payload.candidates || [];
            console.info('[covers] online lookup payload', {
                bookId: this.coverModalBookId,
                candidateCount: incoming.length,
                candidates: incoming.map((c) => ({
                    key: c.key,
                    source: c.source,
                    name: c.name,
                    image_url: c.image_url
                }))
            });
            if (incoming.length === 0) {
                this.ui.coverModalStatus.textContent = 'No online covers found for this title.';
                return;
            }

            const mergedMap = { ...this.coverCandidatesByKey };
            incoming.forEach((c) => {
                if (c && c.key && !mergedMap[c.key]) {
                    mergedMap[c.key] = c;
                }
            });
            const merged = Object.values(mergedMap);
            this.renderCoverCandidates(merged);
            console.info('[covers] online lookup merged', {
                bookId: this.coverModalBookId,
                totalVisibleCandidates: merged.length
            });
            this.ui.coverModalStatus.textContent = `Added ${incoming.length} online candidates.`;
            this.ui.coverModalApply.disabled = false;
        } catch (err) {
            console.error('[covers] online lookup exception', {
                bookId: this.coverModalBookId,
                error: err && err.message ? err.message : String(err)
            });
            this.ui.coverModalStatus.textContent = `Error: ${err.message}`;
            console.error(err);
        } finally {
            this.ui.coverFetchOnline.disabled = false;
            console.info('[covers] online lookup end', {
                bookId: this.coverModalBookId
            });
        }
    },

    async applyCoverSelection() {
        if (!this.auth.authenticated) {
            this.ui.coverModalStatus.textContent = 'Admin login required.';
            return;
        }
        if (!this.coverModalBookId) {
            return;
        }
        const picked = this.ui.coverGrid.querySelector('input[name="cover-candidate"]:checked');
        if (!picked) {
            this.ui.coverModalStatus.textContent = 'Select a cover first.';
            return;
        }

        this.ui.coverModalApply.disabled = true;
        this.ui.coverModalStatus.textContent = 'Applying cover...';

        try {
            const pickedCandidate = this.coverCandidatesByKey[picked.value];
            const response = await fetch(`/api/books/${this.coverModalBookId}/cover`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    key: picked.value,
                    image_url: pickedCandidate && pickedCandidate.remote ? pickedCandidate.image_url : '',
                    write_to_epub: Boolean(this.ui.coverWriteEPUB.checked)
                })
            });
            if (!response.ok) {
                if (response.status === 401) {
                    await this.syncAuthStatus();
                }
                const msg = await response.text();
                throw new Error(msg || `Cover update failed (${response.status})`);
            }

            const payload = await response.json();
            this.coverVersion[this.coverModalBookId] = Date.now();
            this.render(true);
            this.ui.coverModalStatus.textContent = payload.wrote_to_epub
                ? 'Cover updated in cache and EPUB.'
                : 'Cover cache updated.';
        } catch (err) {
            this.ui.coverModalStatus.textContent = `Error: ${err.message}`;
            console.error(err);
        } finally {
            this.ui.coverModalApply.disabled = false;
        }
    },

    async openModal(book) {
        if (!this.auth.authenticated) {
            this.ui.authStatus.textContent = 'Admin login required.';
            return;
        }
        this.modalBookId = book.id;
        this.openLibraryResults = [];
        this.selectedOpenLibrary = null;

        this.ui.modalBook.textContent = `Book #${book.id} | ${book.title || 'Untitled'}`;
        this.ui.modalStatus.textContent = 'Loading metadata directly from EPUB...';
        this.ui.olStatus.textContent = 'Fetch Open Library results to compare fields.';
        this.ui.olResults.innerHTML = '';

        const query = [book.title, book.author].filter(Boolean).join(' ').trim();
        this.ui.olQuery.value = query;

        this.clearRemoteFieldColumn();
        this.ui.modal.classList.remove('hidden');

        try {
            const response = await fetch(`/api/books/${book.id}/metadata/live`);
            if (!response.ok) {
                if (response.status === 401) {
                    await this.syncAuthStatus();
                }
                const msg = await response.text();
                throw new Error(msg || `Live metadata lookup failed (${response.status})`);
            }

            const local = await response.json();
            this.fillLocalFields(local);
            this.ui.modalStatus.textContent = 'Live EPUB metadata loaded.';
        } catch (err) {
            this.ui.modalStatus.textContent = `Error: ${err.message}`;
            console.error(err);
        }
    },

    closeModal() {
        this.modalBookId = null;
        this.ui.modal.classList.add('hidden');
        this.restoreReviewModal();
    },

    fillLocalFields(meta) {
        FIELDS.forEach((f) => {
            if (f.key === 'subjects') {
                const value = Array.isArray(meta.subjects) ? meta.subjects.join(', ') : '';
                this.ui.fieldInputs[f.key].value = value;
                return;
            }
            this.ui.fieldInputs[f.key].value = meta[f.key] || '';
        });
    },

    collectLocalFields() {
        const payload = {};
        FIELDS.forEach((f) => {
            let value = this.ui.fieldInputs[f.key].value || '';
            if (f.key === 'subjects') {
                payload.subjects = value
                    .split(',')
                    .map((v) => v.trim())
                    .filter(Boolean);
                return;
            }
            payload[f.key] = value;
        });
        return payload;
    },

    async handleMetadataSubmit(e) {
        e.preventDefault();
        if (!this.auth.authenticated) {
            this.ui.modalStatus.textContent = 'Admin login required.';
            return;
        }
        if (!this.modalBookId) {
            return;
        }

        const payload = this.collectLocalFields();
        this.ui.modalSave.disabled = true;
        this.ui.modalStatus.textContent = 'Saving all fields to EPUB...';

        try {
            const response = await fetch(`/api/books/${this.modalBookId}/metadata`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
            });

            if (!response.ok) {
                if (response.status === 401) {
                    await this.syncAuthStatus();
                }
                const msg = await response.text();
                throw new Error(msg || `Update failed (${response.status})`);
            }

            const updated = await response.json();
            this.fillLocalFields(updated);
            this.updateBookCardStateFromMetadata(updated);
            this.applyFiltersAndRender();
            this.ui.modalStatus.textContent = 'Saved to EPUB and DB cache.';
            this.closeModal();
        } catch (err) {
            this.ui.modalStatus.textContent = `Error: ${err.message}`;
            console.error(err);
        } finally {
            this.ui.modalSave.disabled = false;
        }
    },

    async fetchOpenLibrary() {
        const query = this.ui.olQuery.value.trim();
        const isbn = (this.ui.fieldInputs.identifier.value || '').trim();
        if (!query && !isbn) {
            this.ui.olStatus.textContent = 'Enter a title/author query or ISBN first.';
            return;
        }

        this.ui.olFetch.disabled = true;
        this.ui.olStatus.textContent = isbn
            ? 'Fetching metadata (prioritizing ISBN lookup)...'
            : 'Fetching metadata...';

        try {
            const params = new URLSearchParams();
            if (query) {
                params.set('q', query);
            }
            if (isbn) {
                params.set('isbn', isbn);
            }
            const response = await fetch(`/api/openlibrary/search?${params.toString()}`);
            if (!response.ok) {
                const msg = await response.text();
                throw new Error(msg || `Open Library lookup failed (${response.status})`);
            }

            const payload = await response.json();
            this.openLibraryResults = payload.results || [];
            this.selectedOpenLibrary = null;
            this.clearRemoteFieldColumn();

            if (this.openLibraryResults.length === 0) {
                this.ui.olStatus.textContent = 'No results found.';
                this.ui.olResults.innerHTML = '';
                return;
            }

            this.ui.olStatus.textContent = `Showing ${this.openLibraryResults.length} of ${payload.num_found || this.openLibraryResults.length} results.`;
            this.renderOpenLibraryResults();
            this.selectOpenLibraryResult(0);
        } catch (err) {
            this.ui.olStatus.textContent = `Error: ${err.message}`;
            this.ui.olResults.innerHTML = '';
            console.error(err);
        } finally {
            this.ui.olFetch.disabled = false;
        }
    },

    renderOpenLibraryResults() {
        this.ui.olResults.innerHTML = this.openLibraryResults.map((r, idx) => `
            <button type="button" class="ol-result-select" data-result-index="${idx}">
                ${this.escapeHTML(r.title || 'Untitled')} | ${this.escapeHTML(r.author || 'Unknown')} | ${this.escapeHTML(r.source || 'unknown')}
            </button>
        `).join('');
    },

    selectOpenLibraryResult(index) {
        const picked = this.openLibraryResults[index];
        if (!picked) {
            return;
        }
        this.selectedOpenLibrary = picked;

        const buttons = this.ui.olResults.querySelectorAll('[data-result-index]');
        buttons.forEach((btn) => {
            btn.classList.toggle('active', Number(btn.dataset.resultIndex) === index);
        });

        FIELDS.forEach((f) => {
            const cell = this.ui.modal.querySelector(`[data-remote="${f.key}"]`);
            if (!cell) {
                return;
            }
            let value = picked[f.key];
            if (f.key === 'subjects') {
                value = Array.isArray(picked.subjects) ? picked.subjects.join(', ') : '';
            }
            cell.textContent = value || '-';
        });
    },

    clearRemoteFieldColumn() {
        FIELDS.forEach((f) => {
            const cell = this.ui.modal.querySelector(`[data-remote="${f.key}"]`);
            if (cell) {
                cell.textContent = '-';
            }
        });
    },

    applyRemoteField(field) {
        if (!this.selectedOpenLibrary) {
            this.ui.modalStatus.textContent = 'Select an Open Library result first.';
            return;
        }

        if (field === 'subjects') {
            const value = Array.isArray(this.selectedOpenLibrary.subjects)
                ? this.selectedOpenLibrary.subjects.join(', ')
                : '';
            this.ui.fieldInputs.subjects.value = value;
        } else {
            this.ui.fieldInputs[field].value = this.selectedOpenLibrary[field] || '';
        }

        this.ui.modalStatus.textContent = `Applied ${field} from Open Library.`;
    },

    updateBookCardStateFromMetadata(meta) {
        if (!this.modalBookId) {
            return;
        }
        const id = this.modalBookId;
        this.allBooks = this.allBooks.map((b) => {
            if (b.id !== id) {
                return b;
            }
            return {
                ...b,
                title: meta.title || b.title,
                author: meta.author || b.author,
                description: meta.description || b.description
            };
        });
        this.filteredBooks = this.filteredBooks.map((b) => {
            if (b.id !== id) {
                return b;
            }
            return {
                ...b,
                title: meta.title || b.title,
                author: meta.author || b.author,
                description: meta.description || b.description
            };
        });
    },

    render(reset = false) {
        if (reset) {
            this.currentIndex = 0;
            this.ui.library.innerHTML = '';
        }

        const entries = this.displayEntries();
        const nextBatch = entries.slice(this.currentIndex, this.currentIndex + this.itemsPerPage);
        const fragment = document.createDocumentFragment();

        nextBatch.forEach((entry) => {
            fragment.appendChild(entry.series
                ? this.buildSeriesCard(entry)
                : this.buildBookCard(entry.book));
        });

        this.ui.library.appendChild(fragment);
        this.currentIndex += this.itemsPerPage;

        if (!entries.length) {
            this.ui.library.innerHTML =
                '<div class="library-empty">No books match the current filters.</div>';
        }
    },

    // displayEntries collapses multi-book series into a single card so a long
    // series reads as one shelf entry instead of a dozen near-identical covers.
    // A series the user has expanded renders its volumes inline.
    displayEntries() {
        if (!this.groupSeries) {
            return this.filteredBooks.map((book) => ({ book }));
        }

        const groups = new Map();
        const entries = [];

        this.filteredBooks.forEach((book) => {
            const info = this.seriesInfo(book);
            if (!info) {
                entries.push({ book });
                return;
            }
            const key = `${(book.author || '').toLowerCase()}|${info.name.toLowerCase()}`;
            if (!groups.has(key)) {
                const group = { series: info.name, author: book.author, books: [], key };
                groups.set(key, group);
                entries.push(group);
            }
            groups.get(key).books.push({ book, index: info.index });
        });

        // A "series" of one is just a book.
        const flattened = [];
        entries.forEach((entry) => {
            if (!entry.series) {
                flattened.push(entry);
                return;
            }
            if (entry.books.length < 2) {
                flattened.push({ book: entry.books[0].book });
                return;
            }
            entry.books.sort((a, b) => (Number(a.index) || 0) - (Number(b.index) || 0));
            flattened.push(entry);
            if (this.expandedSeries[entry.key]) {
                entry.books.forEach((b) => flattened.push({ book: b.book, inSeries: true }));
            }
        });
        return flattened;
    },

    // seriesInfo reads the structured series fields the scanner extracts.
    // Falls back to parsing the title for books indexed before series columns
    // existed, so an un-rescanned library still groups correctly.
    seriesInfo(book) {
        const name = String(book.series || '').trim();
        if (name) {
            return { name, index: this.formatSeriesIndex(book.series_index) };
        }
        const named = SERIES_NAMED_RE.exec(String(book.title || ''));
        if (named && named[1] && !/\d/.test(named[1])) {
            return { name: named[1].trim(), index: named[2] };
        }
        return null;
    },

    // Calibre stores series positions as floats ("1.0"); show "1" but keep
    // genuine half-numbers like "2.5" that mark novellas.
    formatSeriesIndex(raw) {
        const v = String(raw || '').trim();
        if (!v) {
            return '';
        }
        const n = Number(v);
        return Number.isFinite(n) ? String(n) : v;
    },

    buildBookCard(book) {
        const el = document.createElement('div');
        el.className = 'book';
        const coverV = this.coverVersion[book.id] || 0;
        const info = this.seriesInfo(book);
        const badge = info
            ? `<span class="series-badge" title="${this.escapeHTML(info.name)}">${this.escapeHTML(info.name)}${info.index ? ' #' + this.escapeHTML(info.index) : ''}</span>`
            : '';
        el.innerHTML = `
            <a href="/download/${book.id}">
                <img src="/covers/${book.id}.jpg?v=${coverV}"
                     alt="${this.escapeHTML(book.title || '')}"
                     loading="lazy"
                     onerror="this.onerror=null;this.src=App.COVER_PLACEHOLDER;this.classList.add('cover-missing')">
            </a>
            <span class="book-title">${this.escapeHTML(book.title || '')}</span>
            <small>${this.escapeHTML(book.author || '')}</small>
            ${badge}
            <div class="book-actions">
                <a class="book-download" href="/download/${book.id}">Download</a>
                <button type="button" class="book-info" data-book-id="${book.id}">Book Info</button>
                ${this.auth.authenticated ? `<button type="button" class="edit-toggle" data-book-id="${book.id}">Edit Metadata</button>` : ''}
                ${this.auth.authenticated ? `<button type="button" class="change-cover" data-book-id="${book.id}">Change Cover</button>` : ''}
            </div>
        `;
        return el;
    },

    buildSeriesCard(group) {
        const el = document.createElement('div');
        el.className = 'book book-series';
        const expanded = Boolean(this.expandedSeries[group.key]);
        const covers = group.books.slice(0, 3).map((b, i) => {
            const v = this.coverVersion[b.book.id] || 0;
            return `<img class="series-cover series-cover-${i}" src="/covers/${b.book.id}.jpg?v=${v}"
                        alt="" loading="lazy"
                        onerror="this.onerror=null;this.src=App.COVER_PLACEHOLDER">`;
        }).join('');

        el.innerHTML = `
            <div class="series-stack" data-series-toggle="${this.escapeHTML(group.key)}">
                ${covers}
                <span class="series-count">${group.books.length}</span>
            </div>
            <span class="book-title">${this.escapeHTML(group.series)}</span>
            <small>${this.escapeHTML(group.author || '')}</small>
            <span class="series-badge">${group.books.length} books</span>
            <div class="book-actions">
                <button type="button" class="series-expand" data-series-toggle="${this.escapeHTML(group.key)}">
                    ${expanded ? 'Collapse' : 'Show all'}
                </button>
            </div>
        `;
        return el;
    },

    createReviewModal() {
        const modal = document.createElement('div');
        modal.id = 'review-modal';
        modal.className = 'modal hidden';
        modal.innerHTML = `
            <div class="modal-backdrop" data-close-review="1"></div>
            <div class="modal-dialog review-dialog" role="dialog" aria-modal="true" aria-label="Metadata review queue">
                <div class="modal-header">
                    <h2>Metadata Review</h2>
                    <button type="button" id="review-close" class="modal-close" aria-label="Close">&times;</button>
                </div>
                <div class="review-summary" id="review-summary">Loading library quality...</div>
                <div class="review-controls">
                    <select id="review-flag" aria-label="Filter by issue">
                        <option value="">All issues</option>
                    </select>
                    <button type="button" id="review-enrich" title="Look up missing metadata online (dry run)">Dry-run Enrich</button>
                    <button type="button" id="review-enrich-apply" title="Apply high-confidence matches">Apply Enrich</button>
                    <button type="button" id="review-covers" title="Find better cover art online (dry run)">Dry-run Covers</button>
                    <button type="button" id="review-covers-apply" title="Replace low-quality covers with better online art">Upgrade Covers</button>
                    <span class="edit-status" id="review-enrich-status"></span>
                </div>
                <div id="review-list" class="review-list"></div>
                <div class="review-pager">
                    <button type="button" id="review-prev">Previous</button>
                    <span id="review-page-label"></span>
                    <button type="button" id="review-next">Next</button>
                </div>
            </div>
        `;
        document.body.appendChild(modal);

        this.ui.reviewModal = modal;
        this.ui.reviewClose = modal.querySelector('#review-close');
        this.ui.reviewSummary = modal.querySelector('#review-summary');
        this.ui.reviewFlag = modal.querySelector('#review-flag');
        this.ui.reviewList = modal.querySelector('#review-list');
        this.ui.reviewPrev = modal.querySelector('#review-prev');
        this.ui.reviewNext = modal.querySelector('#review-next');
        this.ui.reviewPageLabel = modal.querySelector('#review-page-label');
        this.ui.reviewEnrich = modal.querySelector('#review-enrich');
        this.ui.reviewEnrichApply = modal.querySelector('#review-enrich-apply');
        this.ui.reviewEnrichStatus = modal.querySelector('#review-enrich-status');
        this.ui.reviewCovers = modal.querySelector('#review-covers');
        this.ui.reviewCoversApply = modal.querySelector('#review-covers-apply');

        this.ui.reviewClose.addEventListener('click', () => this.closeReviewModal());
        modal.addEventListener('click', (e) => {
            if (e.target.dataset.closeReview === '1') {
                this.closeReviewModal();
            }
        });
        this.ui.reviewFlag.addEventListener('change', (e) => {
            this.reviewFlag = e.target.value;
            this.reviewPage = 1;
            this.loadReviewQueue();
        });
        this.ui.reviewPrev.addEventListener('click', () => {
            if (this.reviewPage > 1) {
                this.reviewPage -= 1;
                this.loadReviewQueue();
            }
        });
        this.ui.reviewNext.addEventListener('click', () => {
            if (this.reviewPage * 25 < this.reviewTotal) {
                this.reviewPage += 1;
                this.loadReviewQueue();
            }
        });
        this.ui.reviewEnrich.addEventListener('click', () => this.startEnrichment(false));
        this.ui.reviewEnrichApply.addEventListener('click', () => this.startEnrichment(true));
        this.ui.reviewCovers.addEventListener('click', () => this.upgradeCovers(false));
        this.ui.reviewCoversApply.addEventListener('click', () => this.upgradeCovers(true));
        this.ui.reviewList.addEventListener('click', (e) => this.handleReviewListClick(e));
    },

    async openReviewModal() {
        if (!this.auth.authenticated) {
            return;
        }
        this.ui.reviewModal.classList.remove('hidden');
        this.reviewPage = 1;
        await Promise.all([this.loadQualitySummary(), this.loadReviewQueue()]);
        await this.syncEnrichStatus();
    },

    closeReviewModal() {
        this.returnToReview = false;
        this.ui.reviewModal.classList.add('hidden');
        this.stopEnrichPolling();
    },

    // Hide the review queue while an edit or cover modal is open, remembering
    // to come back to it. Stacking the dialogs would leave two scrollable
    // layers fighting for the same backdrop and Escape key.
    hideReviewModalForEditing() {
        this.returnToReview = true;
        this.ui.reviewModal.classList.add('hidden');
        this.stopEnrichPolling();
    },

    // Called from every close path of the edit and cover modals, so the user
    // lands back in the queue whether they saved, cancelled, pressed Escape,
    // or clicked the backdrop.
    restoreReviewModal() {
        if (!this.returnToReview) {
            return;
        }
        this.returnToReview = false;
        this.ui.reviewModal.classList.remove('hidden');
        // The book just edited may no longer belong in the queue.
        this.loadQualitySummary();
        this.loadReviewQueue();
    },

    async loadQualitySummary() {
        try {
            const response = await fetch('/api/admin/quality');
            if (!response.ok) {
                this.ui.reviewSummary.textContent = 'Quality summary unavailable.';
                return;
            }
            const s = await response.json();
            const avg = Math.round(s.avg_quality || 0);
            this.ui.reviewSummary.textContent =
                `${s.total} books | average quality ${avg}/100 | ${s.needs_review} need review | ${s.locked} locked`;

            const flags = Object.entries(s.flag_counts || {})
                .sort((a, b) => b[1] - a[1]);
            const current = this.reviewFlag;
            this.ui.reviewFlag.innerHTML =
                ['<option value="">All issues</option>']
                    .concat(flags.map(([k, v]) =>
                        `<option value="${this.escapeHTML(k)}">${this.escapeHTML(k)} (${v})</option>`))
                    .join('');
            this.ui.reviewFlag.value = current;
        } catch (err) {
            console.error(err);
            this.ui.reviewSummary.textContent = 'Quality summary unavailable.';
        }
    },

    async loadReviewQueue() {
        this.ui.reviewList.innerHTML = '<div class="review-empty">Loading...</div>';
        try {
            const params = new URLSearchParams({ page: String(this.reviewPage), limit: '25' });
            if (this.reviewFlag) {
                params.set('flag', this.reviewFlag);
            }
            const response = await fetch(`/api/admin/review?${params.toString()}`);
            if (!response.ok) {
                this.ui.reviewList.innerHTML = '<div class="review-empty">Failed to load review queue.</div>';
                return;
            }
            const payload = await response.json();
            this.reviewItems = payload.items || [];
            this.reviewTotal = payload.total || 0;
            this.renderReviewList();
        } catch (err) {
            console.error(err);
            this.ui.reviewList.innerHTML = '<div class="review-empty">Failed to load review queue.</div>';
        }
    },

    renderReviewList() {
        if (!this.reviewItems.length) {
            this.ui.reviewList.innerHTML = '<div class="review-empty">Nothing needs review here.</div>';
            this.ui.reviewPageLabel.textContent = '';
            return;
        }

        this.ui.reviewList.innerHTML = this.reviewItems.map((item) => {
            const b = item.book;
            const flags = String(item.review_flags || '')
                .split(',')
                .filter(Boolean)
                .map((f) => `<span class="review-flag">${this.escapeHTML(f)}</span>`)
                .join('');
            return `
                <div class="review-row" data-book-id="${b.id}">
                    <img class="review-cover" src="/covers/${b.id}.jpg" alt=""
                         loading="lazy" onerror="this.style.visibility='hidden'">
                    <div class="review-meta">
                        <div class="review-title">${this.escapeHTML(b.title || 'Untitled')}</div>
                        <div class="review-author">${this.escapeHTML(b.author || 'Unknown Author')}</div>
                        <div class="review-flags">${flags}</div>
                    </div>
                    <div class="review-score" title="Metadata quality score">${item.quality}</div>
                    <div class="review-actions">
                        <button type="button" data-review-edit="${b.id}">Edit</button>
                        <button type="button" data-review-cover="${b.id}">Cover</button>
                        <button type="button" data-review-resolve="${b.id}">Resolve</button>
                        <button type="button" data-review-lock="${b.id}" title="Resolve and never auto-touch this book">Lock</button>
                    </div>
                </div>
            `;
        }).join('');

        const start = (this.reviewPage - 1) * 25 + 1;
        const end = Math.min(this.reviewPage * 25, this.reviewTotal);
        this.ui.reviewPageLabel.textContent = `${start}-${end} of ${this.reviewTotal}`;
    },

    async handleReviewListClick(e) {
        const target = e.target.closest('button');
        if (!target) {
            return;
        }

        const editId = target.dataset.reviewEdit;
        const coverId = target.dataset.reviewCover;
        const resolveId = target.dataset.reviewResolve;
        const lockId = target.dataset.reviewLock;

        if (editId) {
            const book = this.findBookById(editId);
            if (book) {
                this.hideReviewModalForEditing();
                this.openModal(book);
            }
            return;
        }
        if (coverId) {
            const book = this.findBookById(coverId);
            if (book) {
                this.hideReviewModalForEditing();
                this.openCoverModal(book);
            }
            return;
        }
        if (resolveId || lockId) {
            const id = resolveId || lockId;
            const locked = Boolean(lockId);
            target.disabled = true;
            try {
                const response = await fetch(`/api/admin/review/${id}/resolve`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ locked })
                });
                if (!response.ok) {
                    throw new Error(await response.text());
                }
                await Promise.all([this.loadQualitySummary(), this.loadReviewQueue()]);
            } catch (err) {
                console.error(err);
                target.disabled = false;
            }
        }
    },

    findBookById(id) {
        const numeric = Number(id);
        return this.allBooks.find((b) => b.id === numeric)
            || (this.reviewItems.find((i) => i.book.id === numeric) || {}).book;
    },

    async startEnrichment(apply) {
        if (apply && !window.confirm(
            'Apply high-confidence metadata to the database cache?\n\n' +
            'Only exact matches are applied, and only to empty fields. ' +
            'EPUB files are not modified.')) {
            return;
        }

        this.ui.reviewEnrichStatus.textContent = apply ? 'Starting apply...' : 'Starting dry run...';
        try {
            const response = await fetch(`/api/admin/enrich?apply=${apply}&limit=200`, { method: 'POST' });
            if (!response.ok) {
                throw new Error(await response.text());
            }
            this.applyEnrichStatus(await response.json());
            this.startEnrichPolling();
        } catch (err) {
            console.error(err);
            this.ui.reviewEnrichStatus.textContent = `Enrichment failed: ${err.message}`;
        }
    },

    async upgradeCovers(apply) {
        if (apply && !window.confirm(
            'Replace low-quality covers with better artwork found online?\n\n' +
            'Only covers that are clearly an improvement are replaced. ' +
            'This updates the cover cache, not your EPUB files.')) {
            return;
        }

        const buttons = [this.ui.reviewCovers, this.ui.reviewCoversApply];
        buttons.forEach((b) => { b.disabled = true; });
        this.ui.reviewEnrichStatus.textContent = apply
            ? 'Upgrading covers (this takes a moment)...'
            : 'Checking for better covers...';

        try {
            const response = await fetch(`/api/admin/covers/upgrade?apply=${apply}&limit=25`, { method: 'POST' });
            if (!response.ok) {
                throw new Error(await response.text());
            }
            const payload = await response.json();
            const verb = payload.dry_run ? 'could upgrade' : 'upgraded';
            const count = payload.dry_run
                ? payload.results.filter((r) => !r.rejected).length
                : payload.applied;
            this.ui.reviewEnrichStatus.textContent =
                `Covers: ${verb} ${count} of ${payload.examined} examined.`;
            if (!payload.dry_run && payload.applied > 0) {
                payload.results.filter((r) => r.applied).forEach((r) => {
                    this.coverVersion[r.book_id] = (this.coverVersion[r.book_id] || 0) + 1;
                });
                this.render(true);
                this.loadQualitySummary();
            }
        } catch (err) {
            console.error(err);
            this.ui.reviewEnrichStatus.textContent = `Cover upgrade failed: ${err.message}`;
        } finally {
            buttons.forEach((b) => { b.disabled = false; });
        }
    },

    async syncEnrichStatus() {
        try {
            const response = await fetch('/api/admin/enrich/status');
            if (!response.ok) {
                return;
            }
            const status = await response.json();
            this.applyEnrichStatus(status);
            if (status.running) {
                this.startEnrichPolling();
            }
        } catch (err) {
            console.error(err);
        }
    },

    applyEnrichStatus(status) {
        if (!status || !status.phase) {
            this.ui.reviewEnrichStatus.textContent = '';
            return;
        }
        const mode = status.dry_run ? 'dry run' : 'apply';
        if (status.running) {
            this.ui.reviewEnrichStatus.textContent =
                `${mode}: ${status.processed}/${status.total} | applied ${status.applied} | review ${status.queued_for_review}`;
            return;
        }
        this.stopEnrichPolling();
        if (status.error) {
            this.ui.reviewEnrichStatus.textContent = `Failed: ${status.error}`;
            return;
        }
        if (status.phase === 'complete') {
            const verb = status.dry_run ? 'would update' : 'updated';
            this.ui.reviewEnrichStatus.textContent =
                `${mode} complete: ${verb} ${status.applied}, ${status.queued_for_review} queued, ${status.no_match} no match`;
            this.loadQualitySummary();
            this.loadReviewQueue();
        }
    },

    startEnrichPolling() {
        this.stopEnrichPolling();
        this.enrichPollTimer = window.setInterval(() => this.syncEnrichStatus(), 2000);
    },

    stopEnrichPolling() {
        if (this.enrichPollTimer) {
            window.clearInterval(this.enrichPollTimer);
            this.enrichPollTimer = null;
        }
    },

    escapeHTML(value) {
        return String(value)
            .replaceAll('&', '&amp;')
            .replaceAll('<', '&lt;')
            .replaceAll('>', '&gt;')
            .replaceAll('"', '&quot;')
            .replaceAll("'", '&#39;');
    },

    formatDateTime(value) {
        if (!value) {
            return '-';
        }
        const date = new Date(value);
        if (Number.isNaN(date.getTime())) {
            return String(value);
        }
        return date.toLocaleString();
    }
};

App.init();
