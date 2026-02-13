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

const App = {
    allBooks: [],
    filteredBooks: [],
    currentIndex: 0,
    itemsPerPage: 50,
    modalBookId: null,
    coverModalBookId: null,
    openLibraryResults: [],
    selectedOpenLibrary: null,
    rebuildPollTimer: null,
    lastRebuildCompletedAt: '',
    coverVersion: {},
    auth: {
        authenticated: false,
        username: ''
    },

    ui: {
        library: document.getElementById('library'),
        search: document.getElementById('search'),
        rescanBtn: document.getElementById('rescan-btn'),
        rebuildBtn: document.getElementById('rebuild-btn'),
        rebuildStatus: document.getElementById('rebuild-status'),
        authBtn: document.getElementById('auth-btn'),
        authStatus: document.getElementById('auth-status')
    },

    async init() {
        this.createModal();
        this.createCoverModal();
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
        window.addEventListener('scroll', () => this.handleScroll());
        this.ui.rescanBtn.addEventListener('click', () => this.handleRescanClick());
        this.ui.rebuildBtn.addEventListener('click', () => this.handleRebuildClick());
        this.ui.authBtn.addEventListener('click', () => this.handleAuthClick());

        this.ui.library.addEventListener('click', (e) => this.handleLibraryClick(e));

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

        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape' && !this.ui.modal.classList.contains('hidden')) {
                this.closeModal();
            }
            if (e.key === 'Escape' && !this.ui.coverModal.classList.contains('hidden')) {
                this.closeCoverModal();
            }
        });
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
            this.filteredBooks = [...this.allBooks];
            this.ui.search.placeholder = `Search ${this.allBooks.length} books...`;
            this.render(true);
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
            return;
        }
        this.ui.authBtn.textContent = 'Admin Login';
        this.ui.authStatus.textContent = 'Read-only mode.';
        this.ui.rescanBtn.classList.add('hidden');
        this.ui.rebuildBtn.classList.add('hidden');
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
        const term = e.target.value.toLowerCase();
        this.filteredBooks = this.allBooks.filter((b) =>
            (b.title || '').toLowerCase().includes(term) ||
            (b.author || '').toLowerCase().includes(term)
        );
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

    closeCoverModal() {
        this.coverModalBookId = null;
        this.ui.coverModal.classList.add('hidden');
    },

    renderCoverCandidates(candidates) {
        this.ui.coverGrid.innerHTML = candidates.map((c, idx) => `
            <label class="cover-option">
                <input type="radio" name="cover-candidate" value="${this.escapeHTML(c.key)}" ${c.is_current || idx === 0 ? 'checked' : ''}>
                <img src="${this.escapeHTML(c.preview_url)}" alt="${this.escapeHTML(c.name)}">
                <span>${this.escapeHTML(c.name)}</span>
                <small>${c.width}x${c.height} ${this.escapeHTML(c.media_type || '')}${c.is_current ? ' | current' : ''}</small>
            </label>
        `).join('');
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
            const response = await fetch(`/api/books/${this.coverModalBookId}/cover`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    key: picked.value,
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
            this.render(true);
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

        const nextBatch = this.filteredBooks.slice(this.currentIndex, this.currentIndex + this.itemsPerPage);
        const fragment = document.createDocumentFragment();

        nextBatch.forEach((book) => {
            const el = document.createElement('div');
            el.className = 'book';
            const coverV = this.coverVersion[book.id] || 0;
            el.innerHTML = `
                <a href="/download/${book.id}">
                    <img src="/covers/${book.id}.jpg?v=${coverV}"
                         alt="${this.escapeHTML(book.title || '')}"
                         loading="lazy"
                         onerror="this.src='https://via.placeholder.com/150x220?text=No+Cover'">
                </a>
                <span class="book-title">${this.escapeHTML(book.title || '')}</span>
                <small>${this.escapeHTML(book.author || '')}</small>
                <div class="book-actions">
                    <a class="book-download" href="/download/${book.id}">Download</a>
                    ${this.auth.authenticated ? `<button type="button" class="edit-toggle" data-book-id="${book.id}">Edit Metadata</button>` : ''}
                    ${this.auth.authenticated ? `<button type="button" class="change-cover" data-book-id="${book.id}">Change Cover</button>` : ''}
                </div>
            `;
            fragment.appendChild(el);
        });

        this.ui.library.appendChild(fragment);
        this.currentIndex += this.itemsPerPage;
    },

    escapeHTML(value) {
        return String(value)
            .replaceAll('&', '&amp;')
            .replaceAll('<', '&lt;')
            .replaceAll('>', '&gt;')
            .replaceAll('"', '&quot;')
            .replaceAll("'", '&#39;');
    }
};

App.init();
