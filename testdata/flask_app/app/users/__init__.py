from flask import Blueprint

# url_prefix here is overridden by the one given to register_blueprint()
bp = Blueprint("users", __name__, url_prefix="/overridden")

from . import views  # noqa: E402,F401
