# Each bare driver is one executable. The variants of the telescope,
# the focuser, and the dome build from one source each, with the
# variant as a compile definition, because a driver cannot turn on two
# park types, or a timer and an absolute position, at once.
cmake_minimum_required(VERSION 3.16)
project(bare_indi CXX)
set(CMAKE_CXX_STANDARD 17)
find_package(PkgConfig REQUIRED)
pkg_check_modules(INDI REQUIRED libindi)

# libindi.pc names only libindiclient. A driver links libindidriver,
# which also holds main().
function(bare_driver name source)
  add_executable(indi_bare_${name} ${source})
  target_include_directories(indi_bare_${name} PRIVATE ${INDI_INCLUDE_DIRS})
  target_link_libraries(indi_bare_${name} indidriver)
  target_compile_definitions(indi_bare_${name} PRIVATE ${ARGN})
  install(TARGETS indi_bare_${name} DESTINATION bin)
endfunction()

bare_driver(device device.cpp)
bare_driver(ccd ccd.cpp)
bare_driver(filter filter.cpp)
bare_driver(focuser focuser.cpp)
bare_driver(focuser_timer focuser.cpp BARE_TIMER)
bare_driver(rotator rotator.cpp)
bare_driver(dustcap dustcap.cpp)
bare_driver(lightbox lightbox.cpp)
bare_driver(pac pac.cpp)
bare_driver(gps gps.cpp)
bare_driver(dome dome.cpp)
bare_driver(dome_roof dome.cpp BARE_ROOF)
bare_driver(weather weather.cpp)
bare_driver(output output.cpp)
bare_driver(input input.cpp)
bare_driver(receiver receiver.cpp)
foreach(park RA_DEC HA_DEC AZ_ALT RA_DEC_ENCODER AZ_ALT_ENCODER SIMPLE)
  string(TOLOWER ${park} variant)
  bare_driver(telescope_${variant} telescope.cpp BARE_PARK=PARK_${park})
endforeach()
