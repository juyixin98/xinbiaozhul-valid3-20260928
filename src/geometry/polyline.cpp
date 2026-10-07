#include "geometry/polyline.hpp"

#include <cmath>
#include <stdexcept>

namespace mileage {

Polyline::Polyline(std::vector<Vec2> pts) : pts_(std::move(pts)) {
  prefix_len_.assign(pts_.size(), 0.0L);
  seg_len_.assign(segment_count(), 0.0L);
  for (size_t i = 0; i < segment_count(); ++i) {
    const Vec2 d = pts_[i + 1] - pts_[i];
    const cpp_int len2 = dot(d, d);
    const long double l =
        std::sqrt(static_cast<long double>(len2.convert_to<long double>()));
    seg_len_[i] = l;
    prefix_len_[i + 1] = prefix_len_[i] + l;
  }
}

long double Polyline::mileage_at(size_t seg, const Rat& t) const {
  if (seg >= segment_count()) {
    throw std::out_of_range("mileage_at: segment index out of range");
  }
  const long double t_approx = t.num.convert_to<long double>() /
                               t.den.convert_to<long double>();
  return prefix_len_[seg] + t_approx * seg_len_[seg];
}

}  // namespace mileage
