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
    openLibraryResults: [],
    selectedOpenLibrary: null,

    ui: {
        library: document.getElementById('library'),
        search: document.getElementById('search')
    },

    async init() {
        this.createModal();
        this.bindEvents();
        await this.fetchLibrary();
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

        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape' && !this.ui.modal.classList.contains('hidden')) {
                this.closeModal();
            }
        });
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
        const button = e.target.closest('.edit-toggle');
        if (!button) {
            return;
        }

        const id = Number(button.dataset.bookId);
        const book = this.allBooks.find((b) => b.id === id);
        if (!book) {
            return;
        }

        this.openModal(book);
    },

    async openModal(book) {
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
                const msg = await response.text();
                throw new Error(msg || `Update failed (${response.status})`);
            }

            const updated = await response.json();
            this.fillLocalFields(updated);
            this.updateBookCardStateFromMetadata(updated);
            this.render(true);
            this.ui.modalStatus.textContent = 'Saved to EPUB and DB cache.';
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
            el.innerHTML = `
                <a href="/download/${book.id}">
                    <img src="/covers/${book.id}.jpg"
                         alt="${this.escapeHTML(book.title || '')}"
                         loading="lazy"
                         onerror="this.src='https://via.placeholder.com/150x220?text=No+Cover'">
                </a>
                <span class="book-title">${this.escapeHTML(book.title || '')}</span>
                <small>${this.escapeHTML(book.author || '')}</small>
                <div class="book-actions">
                    <a class="book-download" href="/download/${book.id}">Download</a>
                    <button type="button" class="edit-toggle" data-book-id="${book.id}">Edit Metadata</button>
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
