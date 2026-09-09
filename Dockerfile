FROM python:3.12-slim@sha256:78387bc3881b8273120a12ebe6c1ab22b018ccc2c9adf565ae1ac9b536e184ea
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 LOOM_BIND_HOST=0.0.0.0 LOOM_STATE_DIR=/var/lib/loom
WORKDIR /app
COPY pyproject.toml requirements.lock ./
COPY server ./server
RUN pip install --no-cache-dir --constraint requirements.lock . \
    && groupadd --gid 10001 loom \
    && useradd --uid 10001 --gid 10001 --no-create-home loom \
    && mkdir /var/lib/loom \
    && chown 10001:10001 /var/lib/loom \
    && chmod 700 /var/lib/loom
USER 10001:10001
EXPOSE 8000
ENTRYPOINT ["loom"]
CMD ["serve"]
